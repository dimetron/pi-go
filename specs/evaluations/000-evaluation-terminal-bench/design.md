# Design: Terminal-Bench Evaluation for pi-go

## Overview

Implement a Terminal-Bench Pro evaluation harness for pi-go that allows running the 225-task benchmark to measure autonomous terminal interaction capabilities. The solution provides both Harbor Framework integration (for standard benchmark compatibility) and a standalone Go-based runner (for flexibility and no external Python dependencies).

## Detailed Requirements

### Core Requirements

1. **Task Loading** - Load Terminal-Bench Pro tasks from local directory or HuggingFace dataset
2. **Container Management** - Build Docker containers from task Dockerfiles
3. **Agent Execution** - Run pi-go within containers with task instructions
4. **Result Verification** - Execute test scripts and capture pass/fail
5. **Metrics Collection** - Track success rate, timing, token usage
6. **Report Generation** - Produce JSON/HTML results with category breakdown

### User Requirements

1. **Easy Execution** - Single command to run full benchmark or subset
2. **Flexible Configuration** - Select specific tasks, categories, difficulty levels
3. **Progress Tracking** - Real-time progress during long runs
4. **Result Analysis** - Category-by-category breakdown, failure analysis
5. **Leaderboard Ready** - ATIF-compliant output for Harbor compatibility

### Technical Constraints

- Must work with pi-go's existing tool system (bash, read, write, edit, etc.)
- Container timeouts must respect pi-go's 10-minute command limit
- Must handle 225 tasks without overwhelming system resources
- Should support parallel execution (configurable concurrency)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                    terminal-bench-evaluator                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐     │
│  │   Task       │    │  Container   │    │   Result     │     │
│  │   Loader     │───▶│  Manager     │───▶│  Verifier    │     │
│  └──────────────┘    └──────────────┘    └──────────────┘     │
│         │                   │                   │               │
│         ▼                   ▼                   ▼               │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐     │
│  │   Task       │    │   pi-go      │    │   Metrics    │     │
│  │   Pool       │    │   Runner     │    │   Collector  │     │
│  └──────────────┘    └──────────────┘    └──────────────┘     │
│                                                     │           │
│                                                     ▼           │
│                                            ┌──────────────┐     │
│                                            │   Report     │     │
│                                            │   Generator  │     │
│                                            └──────────────┘     │
└─────────────────────────────────────────────────────────────────┘
```

## Components and Interfaces

### 1. Task Loader (`internal/eval/taskloader/`)

**Responsibilities:**
- Load tasks from local directory or HuggingFace
- Parse task metadata (difficulty, category, tags)
- Validate task structure (instruction.md, Dockerfile, test.sh)

**Public API:**

```go
type TaskLoader interface {
    // LoadTasks loads all tasks from the given source
    LoadTasks(ctx context.Context, source TaskSource) ([]Task, error)

    // LoadTask loads a single task by ID
    LoadTask(ctx context.Context, source TaskSource, taskID string) (Task, error)
}

type TaskSource interface {
    // Type returns the source type (local, huggingface)
    Type() string

    // Path returns the path or dataset identifier
    Path() string
}

type Task struct {
    ID          string
    Name        string
    Description string
    Difficulty  Difficulty  // easy, medium, hard
    Category    string
    Tags        []string

    // File paths
    InstructionPath string
    DockerfilePath  string
    TestScriptPath  string

    // Limits
    MaxAgentTimeout time.Duration
    MaxTestTimeout  time.Duration
}

type Difficulty string

const (
    DifficultyEasy   Difficulty = "easy"
    DifficultyMedium Difficulty = "medium"
    DifficultyHard   Difficulty = "hard"
)
```

### 2. Container Manager (`internal/eval/container/`)

**Responsibilities:**
- Build task containers from Dockerfiles
- Manage container lifecycle (start, stop, cleanup)
- Provide execution environment within containers

**Public API:**

```go
type ContainerManager interface {
    // Build builds a container image from a task's Dockerfile
    Build(ctx context.Context, task *Task, options BuildOptions) (ImageID, error)

    // Run starts a container and returns a handle for execution
    Run(ctx context.Context, imageID ImageID, options RunOptions) (ContainerHandle, error)

    // Cleanup removes built images and stopped containers
    Cleanup(ctx context.Context) error
}

type BuildOptions struct {
    // NoCache forces rebuild without cache
    NoCache bool

    // ProgressWriter for build output
    ProgressWriter io.Writer
}

type RunOptions struct {
    // WorkingDir sets the container working directory
    WorkingDir string

    // Env sets environment variables
    Env map[string]string

    // NetworkDisabled disables network access
    NetworkDisabled bool
}

type ContainerHandle interface {
    // Exec executes a command in the container
    Exec(ctx context.Context, cmd ExecCommand) (ExecResult, error)

    // Copy copies files into the container
    Copy(ctx context.Context, files map[string]string, dest string) error

    // Logs returns container logs
    Logs(ctx context.Context) (io.Reader, error)

    // Close stops and removes the container
    Close() error
}

type ExecCommand struct {
    Command   string
    Timeout   time.Duration
    WorkingDir string
    Env       map[string]string
}

type ExecResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
    Timeout  bool
}
```

### 3. pi-go Runner (`internal/eval/runner/`)

**Responsibilities:**
- Execute pi-go agent within a container
- Translate instructions to pi-go tool calls
- Collect execution traces and metrics

**Public API:**

```go
type Runner interface {
    // Run executes pi-go with the given instruction
    Run(ctx context.Context, container ContainerHandle, instruction string, options RunOptions) (*RunResult, error)
}

type RunOptions struct {
    // Model specifies the LLM model to use
    Model string

    // Timeout sets the maximum execution time
    Timeout time.Duration

    // ToolRestrictions limits available tools (nil = all tools)
    ToolRestrictions []string
}

type RunResult struct {
    // Success indicates whether the task was completed
    Success bool

    // Trace contains the execution trace
    Trace *ExecutionTrace

    // Metrics contains execution metrics
    Metrics *ExecutionMetrics
}

type ExecutionTrace struct {
    // Steps records each tool call and result
    Steps []ToolCall

    // TotalTokens used
    TotalTokens int

    // Duration of execution
    Duration time.Duration
}

type ToolCall struct {
    Tool   string
    Input  map[string]interface{}
    Output string
    Error  string
}

type ExecutionMetrics struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int

    // Timing
    FirstTokenLatency time.Duration
    TotalLatency      time.Duration

    // Counts
    TotalSteps    int
    ToolCallCount int
}
```

### 4. Result Verifier (`internal/eval/verifier/`)

**Responsibilities:**
- Execute task verification scripts
- Determine pass/fail based on exit codes
- Capture verification output

**Public API:**

```go
type Verifier interface {
    // Verify executes the task's test script and returns result
    Verify(ctx context.Context, container ContainerHandle, task *Task) (*VerificationResult, error)
}

type VerificationResult struct {
    // Passed indicates whether the task passed
    Passed bool

    // ExitCode from the test script
    ExitCode int

    // Output from the test script
    Output string
}
```

### 5. Evaluation Runner (`internal/eval/evaluator/`)

**Responsibilities:**
- Orchestrate the full evaluation pipeline
- Manage task queue and concurrency
- Aggregate results and generate reports

**Public API:**

```go
type Evaluator struct {
    taskLoader    TaskLoader
    containerMgr  ContainerManager
    runner        Runner
    verifier      Verifier
    metrics       MetricsCollector
}

type EvaluateOptions struct {
    // Tasks specifies which tasks to run (nil = all)
    Tasks []string

    // Categories filters by category
    Categories []string

    // Difficulties filters by difficulty
    Difficulties []Difficulty

    // Concurrency sets max parallel tasks
    Concurrency int

    // Model specifies the LLM model
    Model string

    // OutputPath for results
    OutputPath string
}

type EvaluationResult struct {
    // Summary provides overall statistics
    Summary *Summary

    // TaskResults contains per-task results
    TaskResults map[string]*TaskResult
}

type Summary struct {
    TotalTasks     int
    PassedTasks    int
    FailedTasks    int
    SuccessRate    float64

    // Category breakdown
    ByCategory map[string]*CategorySummary
}

type CategorySummary struct {
    Total   int
    Passed  int
    Success float64
}

type TaskResult struct {
    TaskID      string
    Passed      bool
    Duration    time.Duration
    Error       string
    Trace       *ExecutionTrace
    Metrics     *ExecutionMetrics
}
```

## Data Models

### Evaluation Configuration

```go
type EvalConfig struct {
    // Dataset source
    Dataset DatasetConfig `yaml:"dataset"`

    // pi-go configuration
    Agent AgentConfig `yaml:"agent"`

    // Execution limits
    Limits LimitsConfig `yaml:"limits"`

    // Output settings
    Output OutputConfig `yaml:"output"`
}

type DatasetConfig struct {
    // Source: "local" or "huggingface"
    Source string `yaml:"source"`

    // Path or dataset name
    Path string `yaml:"path"`

    // Task filters
    Filter TaskFilter `yaml:"filter"`
}

type TaskFilter struct {
    // Include specific task IDs
    IDs []string `yaml:"ids"`

    // Include categories
    Categories []string `yaml:"categories"`

    // Include difficulties
    Difficulties []string `yaml:"difficulties"`

    // Exclude tags
    ExcludeTags []string `yaml:"excludeTags"`
}

type AgentConfig struct {
    // Model to use (e.g., "gpt-4o", "claude-sonnet-4-6")
    Model string `yaml:"model"`

    // API key source (env, file, config)
    APIKeySource string `yaml:"apiKeySource"`

    // Allowed tools (empty = all)
    Tools []string `yaml:"tools"`

    // Custom instruction suffix
    Instruction string `yaml:"instruction"`
}

type LimitsConfig struct {
    // Max time for agent per task
    AgentTimeout time.Duration `yaml:"agentTimeout"`

    // Max time for verification per task
    TestTimeout time.Duration `yaml:"testTimeout"`

    // Max concurrent tasks
    Concurrency int `yaml:"concurrency"`

    // Max retries for failed tasks
    MaxRetries int `yaml:"maxRetries"`
}

type OutputConfig struct {
    // Output format: json, html, atif
    Format string `yaml:"format"`

    // Output file path
    Path string `yaml:"path"`

    // Include traces in output
    IncludeTraces bool `yaml:"includeTraces"`
}
```

### ATIF-Compatible Output

For Harbor compatibility, output must conform to ATIF schema:

```go
type ATIFTrajectory struct {
    Version     string         `json:"version"`
    TaskID      string         `json:"task_id"`
    AgentName   string         `json:"agent_name"`
    Model       string         `json:"model"`
    Timestamp   time.Time      `json:"timestamp"`

    Trajectory  []ATIFStep     `json:"trajectory"`
    Metrics     ATIFMetrics    `json:"metrics"`
    Result      ATIFResult     `json:"result"`
}

type ATIFStep struct {
    StepIndex   int             `json:"step_index"`
    ToolCall    ATIFToolCall    `json:"tool_call"`
    Observation string          `json:"observation"`
    Timestamp   time.Time       `json:"timestamp"`
}

type ATIFToolCall struct {
    ToolName string                 `json:"tool_name"`
    Arguments map[string]interface{} `json:"arguments"`
}

type ATIFMetrics struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`

    FirstTokenLatencyMs int64 `json:"first_token_latency_ms"`
    TotalLatencyMs      int64 `json:"total_latency_ms"`

    ToolCalls int `json:"tool_calls"`
}

type ATIFResult struct {
    Success bool   `json:"success"`
    Reward  string `json:"reward"`  // "1" or "0"
    Error   string `json:"error,omitempty"`
}
```

## Error Handling

### Error Categories

| Category | Description | Recovery |
|----------|-------------|----------|
| TaskLoadError | Failed to load task | Skip task, log, continue |
| BuildError | Container build failed | Retry with cache clear |
| ContainerError | Container runtime error | Retry, then skip |
| RunnerError | pi-go execution failed | Retry with timeout |
| TimeoutError | Execution exceeded limit | Mark as failed |
| VerifyError | Test script error | Log output, continue |

### Retry Strategy

```
Initial Failure
    │
    ▼
Retry (up to MaxRetries)
    │
    ├─ Transient error (timeout, network) → Retry
    │
    └─ Persistent error (bad Dockerfile, bad test) → Skip
```

### Logging

- **Structured logging** with levels: DEBUG, INFO, WARN, ERROR
- **Task-scoped context** - all logs include task ID
- **Progress indicators** - show current task, completed count
- **Failure details** - full error chain for debugging

## Acceptance Criteria

### Given-When-Then Format

#### GC1: Full Benchmark Execution

**Given** a valid Terminal-Bench Pro dataset is available
**When** the user runs `pi eval --dataset terminal-bench-pro`
**Then**
- All 225 tasks are executed (or filtered subset)
- Each task runs in an isolated Docker container
- Test script exit code determines pass/fail
- Results are written to `evaluation-results.json`
- Summary shows success rate and category breakdown

#### GC2: Subset Execution

**Given** a valid Terminal-Bench Pro dataset is available
**When** the user runs `pi eval --dataset terminal-bench-pro --categories "software-engineering" --difficulty easy`
**Then**
- Only tasks matching the filter are executed
- Results include only filtered tasks
- Summary reflects only executed tasks

#### GC3: Single Task Execution

**Given** a valid Terminal-Bench Pro dataset is available
**When** the user runs `pi eval --dataset terminal-bench-pro --task build-linux-kernel-qemu`
**Then**
- Only the specified task is executed
- Detailed execution trace is provided
- Pass/fail result is immediate

#### GC4: Result Persistence

**Given** an evaluation has completed
**When** the user examines the output file
**Then**
- JSON output is valid and parseable
- ATIF format is valid (if requested)
- Each task result includes: task_id, passed, duration, error (if any)
- Summary includes: total, passed, failed, success_rate, by_category

#### GC5: Error Recovery

**Given** a task fails due to transient error
**When** the error is timeout or network-related
**Then**
- Task is retried up to MaxRetries times
- Final result reflects retry outcome
- Failure includes retry count in error message

#### GC6: Progress Reporting

**Given** a long-running evaluation
**When** the user is observing the terminal
**Then**
- Progress bar shows: completed/total, current task name
- Estimated time remaining is displayed
- Category completion is shown

## Testing Strategy

### Unit Tests

| Component | Coverage Target |
|-----------|-----------------|
| TaskLoader | 80% - parsing, filtering, validation |
| ContainerManager | 70% - build, run, cleanup logic |
| Runner | 80% - execution, timeout, metrics |
| Verifier | 90% - exit code handling, output capture |
| Evaluator | 70% - orchestration, aggregation |

### Integration Tests

1. **Task Loading** - Load from local test dataset
2. **Container Build** - Build simple test container
3. **Full Pipeline** - Run single task end-to-end

### E2E Tests

1. **Full Benchmark** - Run subset (5-10 tasks) with verification
2. **Parallel Execution** - Verify concurrent task handling

### Test Fixtures

```
testdata/
└── eval/
    ├── minimal-task/
    │   ├── Dockerfile
    │   ├── instruction.md
    │   ├── task.yaml
    │   └── tests/
    │       └── test.sh
    └── multi-task/
        ├── task-1/
        ├── task-2/
        └── ...
```

## Appendices

### Technology Choices

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Container Runtime | Docker | Required by Terminal-Bench, industry standard |
| Task Dataset | HuggingFace `alibabagroup/terminal-bench-pro` | Official source |
| Execution Model | Direct pi-go CLI in container | No Python wrapper needed |
| Result Format | JSON + ATIF | Human and machine readable |

### Alternative Approaches Considered

1. **Harbor Framework Integration**
   - Pros: Standard benchmark harness, leaderboard ready
   - Cons: Requires Python wrapper, Harbor dependency
   - Decision: Support as optional output format, not required

2. **Goroutines for Parallelization**
   - Pros: Native Go concurrency
   - Cons: Container resource management complexity
   - Decision: Use worker pool pattern with configurable concurrency

### Research Findings

- Terminal-Bench Pro tasks require bash, git, apt-get, pip
- pi-go bash tool timeout (10 min) covers most tasks (max_agent_timeout_sec: 180)
- Some tasks need network (clone repos, install packages)
- Test scripts write to /logs/verifier/reward.txt for binary reward

### Limitations

- Requires Docker running on system
- GPU tasks not fully supported (no GPU in standard containers)
- Some tasks may require specific CPU architecture
- Network-dependent tasks need container networking enabled