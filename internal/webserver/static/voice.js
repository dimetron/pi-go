// Browser half of pi's Gemini Live voice session.
//
// The page never talks to Google. It creates a session over REST, opens one
// WebSocket to this server's relay, and from then on:
//
//   up    binary frames of raw little-endian PCM16 mic audio at 16kHz
//   down  binary frames of raw PCM16 output audio at 24kHz, plus small JSON
//         events (ready, transcript deltas, interrupted, turn_complete,
//         go_away, error)
//
// Capture uses an AudioWorklet rather than the deprecated ScriptProcessorNode,
// so the resampling and the 40ms blocking happen off the main thread.

// PCM_BLOCK is how many samples the worklet accumulates before posting one
// frame: 640 samples at 16kHz is 40ms, small enough that barge-in feels
// immediate and large enough that the socket is not woken per render quantum.
const PCM_BLOCK = 640;

// pcm16FromFloat32 converts one Float32 audio chunk to little-endian PCM16 —
// the capture format the relay forwards. Exported for tests.
export function pcm16FromFloat32(f32) {
  const out = new Int16Array(f32.length);
  for (let i = 0; i < f32.length; i++) {
    const s = Math.max(-1, Math.min(1, f32[i]));
    out[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  return out;
}

// float32FromPCM16 converts little-endian PCM16 bytes (the relay's output audio
// frames) to the Float32 samples web audio plays.
export function float32FromPCM16(buf) {
  const i16 = new Int16Array(
    buf.buffer ? buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.byteLength) : buf,
  );
  const out = new Float32Array(i16.length);
  for (let i = 0; i < i16.length; i++) out[i] = i16[i] / (i16[i] < 0 ? 0x8000 : 0x7fff);
  return out;
}

// createVoiceState is the initial fold state.
export function createVoiceState() {
  return {
    status: 'idle', // idle | connecting | active | disconnected | error
    userTranscript: '',
    assistantText: '',
    error: '',
  };
}

// summarizeToolCall renders one relay tool_call event for the transcript panel.
// The server already writes the summary; this only supplies wording for the
// case where it did not, so the panel never shows a blank line.
export function summarizeToolCall(ev) {
  return (ev && ev.summary) || (ev && ev.name) || 'agent tool';
}

// handleVoiceEvent folds one JSON event from the relay into the next state plus
// the actions the caller should render. Audio never reaches this function: it
// arrives as binary frames.
//
// Gemini streams TRUE INCREMENTS for both transcript directions, so the
// userTranscript action carries the accumulated text rather than the delta, and
// `first` says whether the renderer must open a new line for it. Without that
// distinction a single sentence renders as one line per increment, each a
// longer prefix of the last.
export function handleVoiceEvent(state, ev) {
  switch (ev.type) {
    case 'ready':
      return { state: { ...state, status: 'active' }, actions: [{ type: 'status', status: 'active' }] };
    case 'transcript_user_delta': {
      const userTranscript = state.userTranscript + (ev.text || '');
      return {
        state: { ...state, userTranscript },
        actions: [{ type: 'userTranscript', text: userTranscript, first: state.userTranscript === '' }],
      };
    }
    case 'transcript_assistant_delta':
      return {
        state: { ...state, assistantText: state.assistantText + (ev.text || '') },
        actions: [{ type: 'assistantDelta', text: ev.text || '' }],
      };
    case 'tool_call':
      // Tool activity is narration, not conversation: it does not touch the
      // transcript state, so an assistant sentence spanning a tool call stays
      // one line rather than being split by it.
      return { state, actions: [{ type: 'toolCall', name: ev.name || '', summary: summarizeToolCall(ev) }] };
    case 'turn_complete':
      return {
        // Clearing both transcripts at the turn boundary is what makes `first`
        // true again for the next utterance.
        state: { ...state, userTranscript: '', assistantText: '' },
        actions: [{ type: 'assistantFinal', text: state.assistantText }],
      };
    case 'interrupted':
      return { state: { ...state, assistantText: '' }, actions: [{ type: 'interrupt' }] };
    case 'go_away':
      return { state: { ...state, status: 'disconnected' }, actions: [{ type: 'status', status: 'disconnected' }] };
    case 'error':
      return {
        state: { ...state, status: 'error', error: ev.message || 'voice error' },
        actions: [{ type: 'status', status: 'error', message: ev.message }],
      };
    default:
      return { state, actions: [] };
  }
}

// The capture worklet. It only observes the mic; nothing routes to the
// speakers, so the user never hears themselves.
const CAPTURE_WORKLET = `registerProcessor('pi-pcm-capture', class extends AudioWorkletProcessor {
  constructor() { super(); this.buf = []; this.len = 0; }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) {
      this.buf.push(new Float32Array(ch)); this.len += ch.length;
      if (this.len >= ${PCM_BLOCK}) {
        const all = new Float32Array(this.len); let off = 0;
        for (const b of this.buf) { all.set(b, off); off += b.length; }
        this.port.postMessage(all, [all.buffer]);
        this.buf = []; this.len = 0;
      }
    }
    return true;
  }
});`;

// initVoice wires one voice control. deps:
//   onStatus(status, message)  — connection state changed
//   onUserTranscript(text, first) — accumulated user transcript
//   onAssistantDelta(text)     — incremental assistant transcript
//   onAssistantFinal(text)     — the turn's assistant transcript, complete
//   onInterrupt()              — the model was cut off mid-utterance
//   onToolCall(name, summary)  — voice drove the coding agent
//   onLog(stage, detail)       — optional diagnostics
//   terminalSession()          — the PTY session id to bind the voice session
//                                to, so its tools drive the terminal this tab
//                                is showing rather than another tab's
export function initVoice(deps = {}) {
  let state = createVoiceState();
  let sessionID = '';
  let ws = null;
  let micStream = null;
  let capCtx = null;
  let playCtx = null;
  let playTime = 0;
  let sources = [];

  const log = (stage, detail) => deps.onLog && deps.onLog(stage, detail);
  // Resolved per start, not once: a tab that reconnects keeps its session id in
  // sessionStorage, and reading it late is what keeps voice bound to whatever
  // terminal the page is actually attached to now.
  const terminalSession = () =>
    (typeof deps.terminalSession === 'function' ? deps.terminalSession() : deps.terminalSession) || '';

  function setStatus(status, message) {
    state = { ...state, status };
    deps.onStatus && deps.onStatus(status, message);
  }

  function fail(stage, message) {
    log(stage, 'failed: ' + message);
    state = { ...state, status: 'error', error: message };
    deps.onStatus && deps.onStatus('error', message);
    cleanup();
  }

  function fold(next) {
    state = next.state;
    for (const a of next.actions) {
      switch (a.type) {
        case 'status':
          deps.onStatus && deps.onStatus(a.status, a.message);
          break;
        case 'userTranscript':
          deps.onUserTranscript && deps.onUserTranscript(a.text, a.first);
          break;
        case 'assistantDelta':
          deps.onAssistantDelta && deps.onAssistantDelta(a.text);
          break;
        case 'assistantFinal':
          deps.onAssistantFinal && deps.onAssistantFinal(a.text);
          break;
        case 'toolCall':
          deps.onToolCall && deps.onToolCall(a.name, a.summary);
          break;
        case 'interrupt':
          deps.onInterrupt && deps.onInterrupt();
          break;
      }
    }
  }

  // flushPlayback drops every scheduled output chunk. This is barge-in: the
  // relay transport plays a queue of buffers rather than a live stream, so
  // stopping the model means stopping each one already handed to the clock.
  function flushPlayback() {
    for (const src of sources) {
      try { src.stop(); } catch { /* already ended */ }
    }
    sources = [];
    playTime = 0;
  }

  function playChunk(buf) {
    if (!playCtx) return;
    const f32 = float32FromPCM16(new Uint8Array(buf));
    if (!f32.length) return;
    const ab = playCtx.createBuffer(1, f32.length, playCtx.sampleRate);
    ab.getChannelData(0).set(f32);
    const src = playCtx.createBufferSource();
    src.buffer = ab;
    src.connect(playCtx.destination);
    // Schedule right after the previous chunk so streamed audio is gapless.
    const at = Math.max(playCtx.currentTime, playTime);
    src.start(at);
    playTime = at + ab.duration;
    sources.push(src);
    src.onended = () => { sources = sources.filter((s) => s !== src); };
  }

  function cleanup() {
    if (ws) { try { ws.close(); } catch { /* already closed */ } }
    flushPlayback();
    if (capCtx) { try { capCtx.close(); } catch { /* already closed */ } }
    if (playCtx) { try { playCtx.close(); } catch { /* already closed */ } }
    if (micStream) micStream.getTracks().forEach((t) => t.stop());
    ws = null;
    capCtx = null;
    playCtx = null;
    micStream = null;
  }

  async function start(model) {
    if (state.status === 'connecting' || state.status === 'active') {
      log('start', 'a session is already live');
      return;
    }
    state = createVoiceState();
    setStatus('connecting');

    let realtime;
    try {
      const res = await fetch('/api/voice/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model: model || '', terminal: terminalSession() }),
      });
      const body = await res.json();
      if (!res.ok) {
        fail('session', body.error || ('the server refused the session (status ' + res.status + ')'));
        return;
      }
      sessionID = body.id;
      realtime = body.realtime || {};
      log('session', 'created ' + sessionID + ' on ' + body.model +
        (terminalSession() ? ' bound to terminal ' + terminalSession() : ' with NO terminal bound'));
    } catch (e) {
      fail('session', 'could not reach this server to create a voice session: ' + e.message);
      return;
    }

    if (!realtime.ws) {
      fail('session', 'the server returned no relay websocket path');
      return;
    }

    try {
      log('microphone', 'requesting getUserMedia({audio:true})');
      micStream = await navigator.mediaDevices.getUserMedia({ audio: true });
      log('microphone', 'granted');
    } catch (e) {
      fail('microphone', 'microphone permission was refused: ' + e.message);
      return;
    }

    try {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = `${proto}//${location.host}${realtime.ws}?session=${encodeURIComponent(sessionID)}`;
      log('relay', 'opening ' + url);
      ws = new WebSocket(url);
      ws.binaryType = 'arraybuffer';

      ws.onopen = () => log('relay', 'websocket open — waiting for ready');

      ws.onmessage = (ev) => {
        if (ev.data instanceof ArrayBuffer) {
          playChunk(ev.data);
          return;
        }
        let m;
        try { m = JSON.parse(ev.data); } catch { return; }
        // Barge-in has to drop already-scheduled audio before the fold, or the
        // model keeps talking over the user for the length of the queue.
        if (m.type === 'interrupted') flushPlayback();
        fold(handleVoiceEvent(state, m));
      };

      ws.onclose = (ev) => {
        // A relay that closes while still connecting never reached "ready": the
        // server-side dial to Gemini failed, and the close code is the only
        // trace the browser gets. Say so rather than reporting a plain
        // disconnect the user reads as "it just stopped".
        if (state.status === 'connecting') {
          fail('relay', 'the relay closed before the session became ready (code ' +
            ((ev && ev.code) || 0) + (ev && ev.reason ? ', ' + ev.reason : '') +
            ') — check the server log for the Gemini dial failure');
          return;
        }
        log('relay', 'websocket closed (code ' + ((ev && ev.code) || 0) + ')');
        if (state.status === 'active') setStatus('disconnected');
        cleanup();
      };

      ws.onerror = () => fail('relay', "the browser could not reach this server's voice relay");

      // Capture at the Live API's input rate. The browser resamples when the
      // hardware disagrees, which is why the rate is requested here rather than
      // assumed from the device.
      capCtx = new AudioContext({ sampleRate: realtime.inputRate || 16000 });
      const blobURL = URL.createObjectURL(new Blob([CAPTURE_WORKLET], { type: 'text/javascript' }));
      try {
        await capCtx.audioWorklet.addModule(blobURL);
      } finally {
        URL.revokeObjectURL(blobURL);
      }
      const srcNode = capCtx.createMediaStreamSource(micStream);
      const capNode = new AudioWorkletNode(capCtx, 'pi-pcm-capture');
      capNode.port.onmessage = (e) => {
        if (ws && ws.readyState === WebSocket.OPEN) ws.send(pcm16FromFloat32(e.data).buffer);
      };
      srcNode.connect(capNode);

      playCtx = new AudioContext({ sampleRate: realtime.outputRate || 24000 });
      playTime = 0;
      log('audio', `capture at ${capCtx.sampleRate}Hz, playback at ${playCtx.sampleRate}Hz`);
    } catch (e) {
      fail('audio', 'audio setup failed: ' + e.message);
    }
  }

  async function stop() {
    log('stop', 'ending session ' + sessionID);
    cleanup();
    if (sessionID) {
      // Best-effort: the relay's own teardown already frees the slot when this
      // never lands.
      try {
        await fetch('/api/voice/sessions/' + encodeURIComponent(sessionID), { method: 'DELETE' });
      } catch { /* the relay teardown covers it */ }
      sessionID = '';
    }
    setStatus('idle');
  }

  return {
    start,
    stop,
    get status() { return state.status; },
    toggle(model) { return state.status === 'active' || state.status === 'connecting' ? stop() : start(model); },
  };
}

// loadVoiceConfig asks the server whether voice is available. A page that hides
// the control when this reports disabled never offers a button that 503s.
export async function loadVoiceConfig() {
  try {
    const res = await fetch('/api/voice/config');
    const body = await res.json();
    if (!res.ok) {
      // The endpoint is paired-only, because a voice session can type into the
      // coding agent. An expired cookie therefore shows up here rather than as
      // a control that 401s when pressed.
      return { enabled: false, reason: body.error || ('the server refused it (status ' + res.status + ')') };
    }
    return body;
  } catch {
    return { enabled: false, reason: 'the server did not answer /api/voice/config' };
  }
}
