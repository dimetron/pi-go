package webserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The voice bar is three single-purpose buttons: Talk starts, Mute gates the
// mic, End stops. A regression to one toggle, or a button that is not a real
// <button type="button">, would ship silently — nothing else loads this page.
func TestIndexPage_VoiceBarHasTalkMuteEnd(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)

	for _, id := range []string{"voice-talk", "voice-mute", "voice-end"} {
		re := regexp.MustCompile(`<button[^>]*\bid="` + id + `"[^>]*>`)
		m := re.FindString(page)
		if m == "" {
			t.Errorf("index.html has no <button id=%q>", id)
			continue
		}
		if !strings.Contains(m, `type="button"`) {
			t.Errorf("%s is not type=\"button\": %s", id, m)
		}
		if !strings.Contains(m, `title=`) {
			t.Errorf("%s has no title", id)
		}
	}
	if !regexp.MustCompile(`id="voice-mute"[^>]*aria-pressed="false"`).MatchString(page) {
		t.Error("the Mute button must carry aria-pressed so its state is exposed")
	}
	if strings.Contains(page, "voice-toggle") {
		t.Error("index.html still references the retired single voice toggle")
	}
	// Talk must start and only start; End must stop and only stop.
	if !strings.Contains(page, "talkBtn.addEventListener('click', () => voice.start(") {
		t.Error("Talk is not wired to voice.start()")
	}
	if !strings.Contains(page, "endBtn.addEventListener('click', () => voice.stop())") {
		t.Error("End is not wired to voice.stop()")
	}
	if !strings.Contains(page, "muteBtn.addEventListener('click', () => voice.toggleMute())") {
		t.Error("Mute is not wired to voice.toggleMute()")
	}
	if !strings.Contains(page, "onMute(muted)") {
		t.Error("the page does not render mute state via onMute")
	}
}

// The bar sits 3x its original 16px above the bottom edge and the transcript
// panel stacks above it from the same custom property, so a later nudge
// cannot separate them.
func TestVoiceStyles_BarOffsetSharedWithTranscript(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)

	if !strings.Contains(css, "--voice-bar-bottom: 48px;") {
		t.Error("style.css does not define --voice-bar-bottom: 48px")
	}
	bar := cssRule(css, ".voice-bar")
	if !strings.Contains(bar, "bottom: var(--voice-bar-bottom)") {
		t.Errorf(".voice-bar does not take its bottom from --voice-bar-bottom:\n%s", bar)
	}
	if !strings.Contains(bar, "position: fixed") {
		t.Error(".voice-bar must stay position: fixed so it never takes a terminal row")
	}
	panel := cssRule(css, ".voice-transcript")
	if !strings.Contains(panel, "bottom: calc(var(--voice-bar-bottom) + 48px)") {
		t.Errorf(".voice-transcript does not stack above the bar from the same offset:\n%s", panel)
	}
	for _, sel := range []string{".voice-button:disabled", ".voice-button.muted", ".voice-button.active"} {
		if cssRule(css, sel) == "" {
			t.Errorf("style.css has no %s rule", sel)
		}
	}
}

// cssRule returns the body of the first rule whose selector list is exactly sel.
func cssRule(css, sel string) string {
	i := strings.Index(css, "\n"+sel+" {")
	if i < 0 {
		return ""
	}
	rest := css[i:]
	j := strings.Index(rest, "}")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// voice.js is an ES module with no test runner of its own. When node is on
// PATH, load it and check the mute API's idle contract: muting without a live
// session is a no-op, and toggle() is still exported for older callers.
func TestVoiceJS_MuteAPI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	src, err := embeddedStaticFiles.ReadFile("static/voice.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "voice.mjs"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	script := `
import { initVoice, createVoiceState } from './voice.mjs';
const calls = [];
const v = initVoice({ onMute: (m) => calls.push(m), onLog: () => {} });
const must = (cond, msg) => { if (!cond) { console.error('FAIL: ' + msg); process.exit(1); } };
for (const fn of ['start', 'stop', 'toggle', 'mute', 'unmute', 'toggleMute']) {
  must(typeof v[fn] === 'function', fn + ' is not exported');
}
must(v.status === 'idle', 'initial status is ' + v.status);
must(v.muted === false, 'initial muted is ' + v.muted);
must(v.mute() === false, 'mute() with no session must stay false');
must(v.toggleMute() === false, 'toggleMute() with no session must stay false');
must(v.muted === false, 'muted flipped without a live session');
must(calls.length === 0, 'onMute fired without a live session');
must(createVoiceState().status === 'idle', 'createVoiceState status');
// stop() with nothing live is a no-op that still lands on idle and unmuted.
await v.stop();
must(v.status === 'idle', 'status after stop() is ' + v.status);
must(v.muted === false, 'muted after stop() is ' + v.muted);
console.log('ok');
`
	if err := os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "check.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node check failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("unexpected node output:\n%s", out)
	}
}
