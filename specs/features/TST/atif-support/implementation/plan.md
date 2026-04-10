# Implementation Plan: ATIF Export Support

## Checklist

- [ ] Step 1: ATIF data model and JSON serialization
- [ ] Step 2: Event-to-ATIF converter
- [ ] Step 3: Incremental ATIF writer with atomic writes
- [ ] Step 4: Integration into FileService.AppendEvent
- [ ] Step 5: Session resume — rebuild ATIF from existing events
- [ ] Step 6: Subagent trajectory linking
- [ ] Step 7: End-to-end integration test and validation

---

## Step 1: ATIF Data Model and JSON Serialization

**Objective:** Define Go structs for the ATIF v1.6 schema and verify they serialize to spec-compliant JSON.

**Implementation guidance:**
- Create `internal/atif/types.go` with all ATIF structs: `Trajectory`, `AgentInfo`, `Step`, `ToolCall`, `Observation`, `ObservationResult`, `Metrics`, `ContentPart`
- Use `json:"field_name,omitempty"` tags matching the ATIF spec field names exactly
- `Step.Message` should be `any` type to support both plain string and `[]ContentPart` array (v1.6 multimodal)
- `SchemaVersion` should default to `"ATIF-v1.6"`
- Include `Extra map[string]any` fields at every level for extensibility

**Test requirements:**
- Test that each struct serializes to the expected JSON field names
- Test round-trip: marshal → unmarshal → compare
- Test that `omitempty` correctly omits zero-value optional fields
- Test `Step.Message` serializes correctly as both a plain string and a ContentPart array
- Validate a complete example trajectory matches the ATIF spec's example format

**Integration:** These types are the foundation for all subsequent steps. No wiring needed yet.

**Demo:** Run `go test ./internal/atif/...` — all type serialization tests pass. Print a sample ATIF JSON document to stdout that matches the spec.

---

## Step 2: Event-to-ATIF Converter

**Objective:** Convert `session.Event` objects into ATIF `Step` objects with correct field mapping.

**Implementation guidance:**
- Create `internal/atif/convert.go` with a `ConvertEvent(event *session.Event, stepID int) []Step` function
- Handle all event Author values: `"user"` → `"user"`, `"model"` → `"agent"`, `"system"` → `"system"`
- Iterate `event.Content.Parts`:
  - `Part.Text` → accumulate into message text
  - `Part.FunctionCall` → append to `step.ToolCalls` with `ID` → `tool_call_id`, `Name` → `function_name`, `Args` → `arguments`
  - `Part.FunctionResponse` → build `step.Observation` with results, using `Name` as `source_call_id` (or matching to prior tool call ID if available)
- Single text part → plain string message; multiple text parts → `[]ContentPart`
- Set `step.Timestamp` to `event.Timestamp.Format(time.RFC3339Nano)`
- Return a slice of steps (usually 1, but could be 0 if event has no meaningful content)

**Test requirements:**
- Test user text message → user step with string message
- Test model text response → agent step
- Test model with single tool call → agent step with `tool_calls[1]`
- Test model with multiple tool calls → agent step with `tool_calls[N]`
- Test tool response event → system step with `observation.results`
- Test mixed content (text + tool calls in same event) → single step with both
- Test nil content / empty parts → returns empty slice
- Test timestamp formatting is ISO 8601

**Integration:** Builds on Step 1 types. Used by the Writer in Step 3.

**Demo:** Write a test that converts a realistic sequence of `session.Event` objects and prints the resulting ATIF JSON — visually confirm it matches expected ATIF structure.

---

## Step 3: Incremental ATIF Writer with Atomic Writes

**Objective:** Implement `Writer` that maintains an in-memory `Trajectory` and writes the full JSON file atomically after each appended event.

**Implementation guidance:**
- Create `internal/atif/writer.go` with:
  - `SessionMeta` struct: `SessionID`, `AgentName`, `Model`, `WorkDir`
  - `Writer` struct: `filePath`, `trajectory`, `stepCounter`, `mu sync.Mutex`
  - `NewWriter(filePath string, meta SessionMeta) (*Writer, error)` — initializes trajectory with schema version, session ID, agent info
  - `AppendEvent(event *session.Event) error` — converts event via `ConvertEvent`, appends steps, increments counter, calls `flush()`
  - `SetSubagentRef(toolCallID string, refPath string)` — updates an existing observation result with `subagent_trajectory_ref` (used in Step 6)
  - `Close() error` — final flush, no-op if nothing pending
- `flush()` method: marshal trajectory to JSON (indented), write to temp file in same directory, `os.Rename` to target path
- Use `sync.Mutex` to protect concurrent access (though typically single-goroutine)

**Test requirements:**
- Test `NewWriter` creates valid initial trajectory JSON on disk
- Test `AppendEvent` produces valid JSON after each call (parse the file after each append)
- Test multiple sequential appends produce correct `step_id` ordering (1, 2, 3...)
- Test atomic write: verify temp file is cleaned up, target file has final content
- Test `Close` on empty writer (no events) produces valid minimal trajectory
- Test concurrent `AppendEvent` calls don't corrupt the file (mutex test)

**Integration:** Builds on Step 1 types and Step 2 converter.

**Demo:** Create a Writer, append 5 events, read back the file — it's valid ATIF JSON with 5+ steps.

---

## Step 4: Integration into FileService.AppendEvent

**Objective:** Wire the ATIF Writer into the session persistence layer so every session automatically produces ATIF output.

**Implementation guidance:**
- Add `atifWriter *atif.Writer` field to the `fileSession` struct (the per-session state in `FileService`)
- In `FileService.CreateSession` (or the internal session creation path): create an `atif.Writer` targeting `<sessionDir>/trajectory.atif.json`, passing session meta (ID, AppName, Model, WorkDir)
- In `FileService.AppendEvent`, after the JSONL write succeeds (around line 269 in store.go): call `fs.atifWriter.AppendEvent(event)`. On error, log `slog.Warn` and continue.
- Ensure partial events are already filtered before reaching the ATIF writer (they are — line 233)
- In session cleanup/close paths: call `atifWriter.Close()` if it exists

**Test requirements:**
- Test that creating a session produces a `trajectory.atif.json` file in the session directory
- Test that appending events via `FileService.AppendEvent` updates the ATIF file
- Test that ATIF write failure doesn't cause `AppendEvent` to return an error
- Test that the ATIF file is valid JSON after each append

**Integration:** Wires Step 3 Writer into the existing session persistence pipeline.

**Demo:** Start a pi-go session (or use test harness), send a message, verify `trajectory.atif.json` appears in the session directory with the correct content. Run `cat ~/.pi-go/sessions/<id>/trajectory.atif.json | python3 -m json.tool` to validate.

---

## Step 5: Session Resume — Rebuild ATIF from Existing Events

**Objective:** When a session is loaded/resumed, rebuild the ATIF trajectory from all existing events so the file stays complete.

**Implementation guidance:**
- In `FileService.loadSession` (or equivalent session load path): after loading events from JSONL, create a new `atif.Writer` and replay all existing non-partial events through it via `AppendEvent`
- This ensures the ATIF file is rebuilt from scratch on resume, covering cases where the file was corrupted or deleted
- The writer's `stepCounter` will be set correctly after replaying all events
- Store the writer on the `fileSession` so subsequent `AppendEvent` calls continue incrementally
- Handle the branching case: only replay events from the active branch (main branch)

**Test requirements:**
- Test: create session, append 5 events, close. Reload session, verify ATIF file has all 5 events' steps
- Test: create session, append events, delete ATIF file, reload session — ATIF file is recreated
- Test: resumed session continues step_id numbering correctly after replay
- Test: append new events after resume — they appear in ATIF with correct sequential step_ids

**Integration:** Extends Step 4 to handle the session resume lifecycle.

**Demo:** Start a session, send a few messages, exit. Resume the session with `pi-go --resume`, send another message. The `trajectory.atif.json` contains all steps from both the original and resumed session.

---

## Step 6: Subagent Trajectory Linking

**Objective:** When a subagent completes, link its ATIF trajectory from the parent's observation via `subagent_trajectory_ref`.

**Implementation guidance:**
- In the subagent tool result handler (where `FunctionResponse` for subagent calls is constructed): extract the subagent's session directory path from the result metadata
- Compute the relative path from the parent session directory to the subagent's `trajectory.atif.json`
- After the parent's `AppendEvent` processes the tool response, call `writer.SetSubagentRef(toolCallID, relativePath)` to update the observation result
- `SetSubagentRef` finds the matching `ObservationResult` by `source_call_id` and sets `subagent_trajectory_ref`, then flushes
- If the subagent session path is not available (error case), skip — the observation is still recorded with content only

**Test requirements:**
- Test: mock a subagent tool call and response, verify the parent ATIF has `subagent_trajectory_ref` in the observation result
- Test: verify the ref path is relative (not absolute) for portability
- Test: missing subagent path gracefully omits the ref field
- Test: the subagent's own `trajectory.atif.json` is a valid standalone ATIF document

**Integration:** Extends Step 4 with subagent awareness. Requires understanding the subagent tool response format.

**Demo:** Trigger a subagent (e.g., explore agent) during a session. Verify the parent's ATIF links to the subagent's ATIF file, and both are valid ATIF documents.

---

## Step 7: End-to-End Integration Test and Validation

**Objective:** Validate the complete ATIF export pipeline with a realistic session scenario and ensure spec compliance.

**Implementation guidance:**
- Create an integration test (build-tagged `//go:build e2e`) that:
  1. Creates a session via `FileService`
  2. Simulates a realistic event sequence: user message → agent response with tool calls → tool results → agent follow-up → user message → agent final response
  3. Reads back the `trajectory.atif.json` file
  4. Validates all required ATIF fields are present and correctly typed
  5. Validates step_id ordering is sequential starting from 1
  6. Validates source values are only `"user"`, `"agent"`, or `"system"`
  7. Validates tool_call_id linkage: every `source_call_id` in observations matches a `tool_call_id` in a prior step
- Add a JSON schema validation test if a formal ATIF JSON schema is available from Harbor
- Verify the output can be parsed by a simple JSON consumer (no custom deserialization needed)

**Test requirements:**
- Full event sequence test (user → agent → tools → agent → user → agent)
- Schema field presence validation
- Step ordering validation
- Tool call ↔ observation linkage validation
- File size sanity check (reasonable for the event count)
- Malformed event resilience (skip gracefully, don't crash)

**Integration:** Validates all prior steps working together.

**Demo:** Run the e2e test, print the full ATIF JSON output, and show it passes all validation checks. Optionally pipe through `python3 -c "import json,sys; json.load(sys.stdin); print('Valid JSON')"` to confirm.
