# Plan: Better Completion Commands

## Implementation Checklist

- [ ] **Step 1**: Create completion engine (`completion.go`)
  - Define completion types and data structures
  - Implement `Complete()` function that returns all matching candidates
  - Implement command, skill, and spec matching functions
  - **Test**: Unit tests for matching functions

- [ ] **Step 2**: Add completion state to model
  - Add `completion *CompleteResult` field to model
  - Add `completionMode bool` field
  - Add `selectedIndex int` field
  - **Test**: Model compiles and passes existing tests

- [ ] **Step 3**: Add Skills to Config
  - Add `Skills []extension.Skill` field to `Config` struct
  - Update CLI to load skills and pass to TUI
  - **Test**: Skills are passed through correctly

- [ ] **Step 4**: Implement skill matching
  - Create `matchingSkills()` function in completion.go
  - Filter skills by prefix (case-insensitive)
  - Return `CompletionCandidate` slice
  - **Test**: Unit tests with mock skills

- [ ] **Step 5**: Implement spec matching
  - Reuse `listAvailableSpecs()` from run.go or create shared utility
  - Create `matchingSpecs()` function in completion.go
  - Filter specs by prefix
  - **Test**: Unit tests with mock directory structure

- [ ] **Step 6**: Wire up completion to TAB handler
  - Update `handleKey` to use new completion engine
  - Show all matches instead of single
  - Handle completion mode toggle
  - **Test**: Existing tests pass

- [ ] **Step 7**: Add completion cycling
  - Handle Shift+Tab to cycle backwards
  - Update visual indicator for selected item
  - Store selected index in model
  - **Test**: Cycling tests

- [ ] **Step 8**: (Optional) Add completion popup UI
  - Add popup overlay for multiple matches
  - Arrow key navigation in popup
  - Enter to select
  - **Test**: Manual testing

## Dependencies

- Step 1: No new dependencies
- Step 2: Step 1 complete
- Step 3: Step 2 complete, uses extension package (already exists)
- Step 4: Step 1, 3 complete
- Step 5: Step 1, 3 complete, uses run.go (already imported in tui package)
- Step 6: Steps 1-5 complete
- Step 7: Step 6 complete
- Step 8: Step 7 complete (optional enhancement)

## Implementation Guidance

### Step 1: completion.go Structure

```go
package tui

// CompletionType identifies what kind of completion to perform
type CompletionType int

const (
    CompletionTypeNone CompletionType = iota
    CompletionTypeCommand
    CompletionTypeSkill
    CompletionTypeSpec
)

// CompletionCandidate represents a single completion option
type CompletionCandidate struct {
    Text        string
    Description string
    Type        CompletionType
}

// CompleteResult holds all completion results
type CompleteResult struct {
    Candidates []CompletionCandidate
    Selected   int
    Type       CompletionType
}

// Complete returns completion candidates for the given input
func Complete(input string, skills []extension.Skill, workDir string) *CompleteResult
```

### Step 2: Model Changes

```go
type model struct {
    // ... existing fields ...
    
    // Completion state
    completion *CompleteResult
    completionMode bool
    selectedIndex int
}
```

### Step 3: Config Changes

```go
type Config struct {
    // ... existing fields ...
    Skills []extension.Skill
}
```

### Step 4-5: Matching Functions

```go
// matchingCommands returns all command candidates matching prefix
func matchingCommands(prefix string) []CompletionCandidate

// matchingSkills returns all skill candidates matching prefix
func matchingSkills(prefix string, skills []extension.Skill) []CompletionCandidate

// matchingSpecs returns all spec candidates matching prefix
func matchingSpecs(prefix string, workDir string) []CompletionCandidate
```

### Step 6-7: Key Handling

In `handleKey`, replace current completion logic:

```go
case key.Code == tea.KeyTab:
    if m.completionMode {
        // Cycle through matches
        m.selectedIndex = (m.selectedIndex + 1) % len(m.completion.Candidates)
    } else if m.input == "/" {
        m.showCommandList()
    } else {
        // Try completion
        m.completion = Complete(m.input, m.cfg.Skills, m.cfg.WorkDir)
        if len(m.completion.Candidates) == 1 {
            m.input = m.completion.Candidates[0].Text
            m.cursorPos = len(m.input)
        } else if len(m.completion.Candidates) > 1 {
            m.completionMode = true
            m.selectedIndex = 0
        }
    }
```

## Test Requirements

Each step should include unit tests:
- Step 1: Test completion type detection, candidate generation
- Step 4: Test skill matching with various inputs
- Step 5: Test spec matching with mock directory
- Step 7: Test cycling wraps correctly

Run tests after each step: `go test ./internal/tui/...`