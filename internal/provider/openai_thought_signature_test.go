package provider

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// sigB64 is a stand-in for the opaque blob Gemini 3 returns. Its contents are
// never interpreted — only that it survives the round trip byte for byte.
var (
	sigBytes = []byte("thought-signature-bytes")
	sigB64   = base64.StdEncoding.EncodeToString(sigBytes)
)

func TestOaiThoughtSignature(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []byte
	}{
		{
			name: "gemini 3 tool call",
			raw: `{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"},` +
				`"extra_content":{"google":{"thought_signature":"` + sigB64 + `"}}}`,
			want: sigBytes,
		},
		{
			// Every provider that is not Gemini 3: no signature, no field.
			name: "plain openai tool call",
			raw:  `{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"}}`,
			want: nil,
		},
		{
			name: "extra_content from another vendor",
			raw:  `{"id":"c1","extra_content":{"other":{"thought_signature":"` + sigB64 + `"}}}`,
			want: nil,
		},
		{name: "empty raw json", raw: "", want: nil},
		{name: "malformed json", raw: `{"id":`, want: nil},
		{
			name: "undecodable base64",
			raw:  `{"id":"c1","extra_content":{"google":{"thought_signature":"not!base64"}}}`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oaiThoughtSignature(tt.raw)
			if string(got) != string(tt.want) {
				t.Errorf("oaiThoughtSignature() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenaiThoughtSignatures(t *testing.T) {
	signed := genai.NewPartFromFunctionCall("bash", map[string]any{"cmd": "ls"})
	signed.FunctionCall.ID = "c1"
	signed.ThoughtSignature = sigBytes

	unsigned := genai.NewPartFromFunctionCall("read", nil)
	unsigned.FunctionCall.ID = "c2"

	// A signature with no call ID cannot be matched back to a call, so it is
	// dropped rather than keyed under "".
	orphan := genai.NewPartFromFunctionCall("orphan", nil)
	orphan.ThoughtSignature = sigBytes

	parts := []*genai.Part{nil, {Text: "hello"}, signed, unsigned, orphan}

	got := genaiThoughtSignatures(parts)
	if len(got) != 1 {
		t.Fatalf("got %d signatures, want 1: %v", len(got), got)
	}
	if string(got["c1"]) != string(sigBytes) {
		t.Errorf("signature for c1 = %q, want %q", got["c1"], sigBytes)
	}
}

func TestGenaiThoughtSignatures_NoneReturnsNil(t *testing.T) {
	p := genai.NewPartFromFunctionCall("bash", nil)
	p.FunctionCall.ID = "c1"
	if got := genaiThoughtSignatures([]*genai.Part{p}); got != nil {
		t.Errorf("got %v, want nil for parts carrying no signature", got)
	}
}

// TestOaiToolCallMessages_ReplaysThoughtSignature is the regression this whole
// path exists for: Gemini 3 answers a replayed call with
// "400 Function call is missing a thought_signature" unless the signature it
// issued comes back on the assistant message.
func TestOaiToolCallMessages_ReplaysThoughtSignature(t *testing.T) {
	calls := []*genai.FunctionCall{
		{ID: "c1", Name: "bash", Args: map[string]any{"cmd": "ls"}},
		{ID: "c2", Name: "read", Args: map[string]any{"path": "/tmp"}},
	}
	responses := map[string]*genai.FunctionResponse{
		"c1": {ID: "c1", Response: map[string]any{"output": "a.txt"}},
		"c2": {ID: "c2", Response: map[string]any{"output": "ok"}},
	}
	// Only the first call carries a signature, so the second must marshal
	// without an extra_content key at all.
	signatures := map[string][]byte{"c1": sigBytes}

	messages := oaiToolCallMessages(nil, calls, responses, signatures)
	if len(messages) == 0 || messages[0].OfAssistant == nil {
		t.Fatal("expected an assistant message first")
	}
	b, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var got struct {
		ToolCalls []struct {
			ID           string `json:"id"`
			ExtraContent *struct {
				Google struct {
					ThoughtSignature string `json:"thought_signature"`
				} `json:"google"`
			} `json:"extra_content"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal assistant message: %v\n%s", err, b)
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2:\n%s", len(got.ToolCalls), b)
	}
	if got.ToolCalls[0].ExtraContent == nil {
		t.Fatalf("c1 lost its thought signature:\n%s", b)
	}
	if sig := got.ToolCalls[0].ExtraContent.Google.ThoughtSignature; sig != sigB64 {
		t.Errorf("c1 thought_signature = %q, want %q", sig, sigB64)
	}
	if got.ToolCalls[1].ExtraContent != nil {
		t.Errorf("c2 gained an extra_content it was never given:\n%s", b)
	}
	// A provider that never sends signatures must see a byte-identical
	// request to the one it saw before this path existed.
	if strings.Contains(string(b), `"extra_content":null`) {
		t.Errorf("null extra_content leaked into the request:\n%s", b)
	}
}

func TestOaiToolCallMessages_NoSignaturesSendsNoExtraContent(t *testing.T) {
	calls := []*genai.FunctionCall{{ID: "c1", Name: "bash", Args: map[string]any{"cmd": "ls"}}}
	messages := oaiToolCallMessages(nil, calls, map[string]*genai.FunctionResponse{}, nil)
	b, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "extra_content") {
		t.Errorf("extra_content sent for a provider that issued no signature:\n%s", b)
	}
}

func TestAccumulateOaiToolCallSignature(t *testing.T) {
	acc := map[int64]map[string]any{}

	// An empty signature must not conjure an accumulator entry: a nil-signature
	// delta is what every non-Gemini provider streams.
	accumulateOaiToolCallSignature(acc, 0, nil)
	if len(acc) != 0 {
		t.Fatalf("empty signature created an entry: %v", acc)
	}

	accumulateOaiToolCall(acc, 0, "c1", "bash", `{"cmd":"ls"}`)
	accumulateOaiToolCallSignature(acc, 0, sigBytes)
	if got, _ := acc[0]["thought_signature"].([]byte); string(got) != string(sigBytes) {
		t.Errorf("thought_signature = %q, want %q", got, sigBytes)
	}

	// The signature can also arrive before any function delta for that index.
	accumulateOaiToolCallSignature(acc, 1, sigBytes)
	accumulateOaiToolCall(acc, 1, "c2", "read", "")
	if got, _ := acc[1]["thought_signature"].([]byte); string(got) != string(sigBytes) {
		t.Errorf("out-of-order signature lost: %q", got)
	}
	if acc[1]["id"] != "c2" {
		t.Errorf("id = %v, want c2 (accumulator entry was clobbered)", acc[1]["id"])
	}
}

func TestBuildOaiFinalResponse_CarriesThoughtSignature(t *testing.T) {
	state := &oaiStreamState{toolCalls: map[int64]map[string]any{
		0: {"id": "c1", "name": "bash", "arguments": `{"cmd":"ls"}`, "thought_signature": sigBytes},
		1: {"id": "c2", "name": "read", "arguments": "{}"},
	}}

	resp := buildOaiFinalResponse(state)
	if resp.Content == nil || len(resp.Content.Parts) != 2 {
		t.Fatalf("got %v parts, want 2", resp.Content)
	}
	if string(resp.Content.Parts[0].ThoughtSignature) != string(sigBytes) {
		t.Errorf("part 0 signature = %q, want %q", resp.Content.Parts[0].ThoughtSignature, sigBytes)
	}
	if resp.Content.Parts[1].ThoughtSignature != nil {
		t.Errorf("part 1 gained a signature: %q", resp.Content.Parts[1].ThoughtSignature)
	}
}

// TestThoughtSignatureRoundTrip walks the full loop the 400 happens in: a tool
// call arrives from the model, becomes a genai part, and is replayed on the
// next request.
func TestThoughtSignatureRoundTrip(t *testing.T) {
	raw := `{"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"},` +
		`"extra_content":{"google":{"thought_signature":"` + sigB64 + `"}}}`

	part := genai.NewPartFromFunctionCall("bash", map[string]any{"cmd": "ls"})
	part.FunctionCall.ID = "c1"
	part.ThoughtSignature = oaiThoughtSignature(raw)

	contents := []*genai.Content{
		{Role: string(genai.RoleUser), Parts: []*genai.Part{{Text: "list files"}}},
		{Role: string(genai.RoleModel), Parts: []*genai.Part{part}},
		{Role: string(genai.RoleUser), Parts: []*genai.Part{
			genai.NewPartFromFunctionResponse("bash", map[string]any{"output": "a.txt"}),
		}},
	}
	contents[2].Parts[0].FunctionResponse.ID = "c1"

	messages, _ := oaiContentsToMessages(contents, nil)
	b, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	if !strings.Contains(string(b), sigB64) {
		t.Errorf("signature did not survive the round trip:\n%s", b)
	}
}
