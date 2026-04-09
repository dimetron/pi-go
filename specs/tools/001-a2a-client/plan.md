# Plan: A2A Client Tool

## Vertical Slices

---

### Slice 1: A2A Config Structs

**What to implement:**
- Add to `internal/config/config.go`:
  ```go
  type A2AAgentConfig struct {
      Name string `json:"name"`
      URL  string `json:"url"`
  }
  type A2AConfig struct {
      Agents []A2AAgentConfig `json:"agents,omitempty"`
  }
  ```
- Add `A2A *A2AConfig` field to `Config` struct

**Verification:** `go build ./internal/config/...`

**Dependencies:** None

- [x] Step 1: Add A2A Config Structs

---

### Slice 2: A2A ClientCache + Tool Types

**What to implement:**
- Create `internal/tools/a2a.go`:
  - `A2AInput`, `A2AOutput` types
  - `ClientCache` struct with `GetClient()` and `SendMessage()` methods
  - `NewA2ATool()` function using `newTool[TArgs, TResults]`
  - `A2ATools()` function returning `[]tool.Tool`
  - Dynamic tool description with available agent names
  - Aliases: `agent_name` → `agent`, `message` → `prompt`, `input` → `prompt`

**Verification:** `go build ./internal/tools/...`

**Dependencies:** Slice 1

- [x] Step 1: Add A2A Config Structs
- [x] Step 2: Add A2A ClientCache + Tool Types

---

### Slice 3: Wire A2A Tools into Agent

**What to implement:**
- Modify `internal/agent/agent.go`:
  - In `NewAgent()` / `NewAgentWithCallbacks()`, build A2A tools from config
  - Pass A2A tools to `llmagent.New()` via `Config.Toolsets`

**Verification:** `go build ./internal/agent/...`

**Dependencies:** Slice 2

- [x] Step 1: Add A2A Config Structs
- [x] Step 2: Add A2A ClientCache + Tool Types
- [x] Step 3: Wire A2A Tools into Agent

---

### Slice 4: Add Dependency

**What to implement:**
- Add `github.com/a2aproject/a2a-go/v2` to `go.mod`
- Run `go mod tidy`

**Verification:** `go mod tidy && go build ./...`

**Dependencies:** None

- [x] Step 1: Add A2A Config Structs
- [x] Step 2: Add A2A ClientCache + Tool Types
- [x] Step 3: Wire A2A Tools into Agent
- [x] Step 4: Add Dependency

---

### Slice 5: Unit Tests

**What to implement:**
- Create `internal/tools/a2a_test.go`:
  - `TestClientCacheGetClient` — verify lazy client creation
  - `TestSendMessageNonStreaming` — mock A2A server, verify result extraction
  - `TestSendMessageStreaming` — mock A2A server with SSE, verify event accumulation
  - `TestSendMessageUnknownAgent` — verify error result
  - `TestNewA2ATool` — verify tool creation and description

**Verification:** `go test ./internal/tools/... -run A2A -v`

**Dependencies:** Slice 2

---

## Build/Test Commands (from research)

- **build**: `go build ./...`
- **test**: `go test ./...` or `go test ./internal/tools/...`
- **vet**: `go vet ./...`
