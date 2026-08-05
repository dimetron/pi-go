# Rough Idea

temporal-knowledge-session-memory - # PRD: Temporal Knowledge Memory for Coding Agents **Status:** Draft v0.1 **Author:
** Dmytro Rashko + ChatGPT Discussion **Purpose:** Design a long-term memory subsystem for coding agents (Codex, Py, Go
ADK agents, etc.) that continuously learns from execution while preventing hallucinations and preserving engineering
knowledge. --- # Vision Traditional coding agents remember conversations. This system remembers **knowledge**. Rather
than storing raw conversations, tool calls, or execution traces forever, the system continuously extracts engineering
knowledge and builds a temporal knowledge graph. Every future session starts with accumulated project knowledge instead
of raw chat history. --- # Goals - Learn continuously from coding sessions - Preserve engineering decisions - Capture
reusable knowledge - Track temporal evolution of knowledge - Record provenance for every fact - Prevent hallucinations
from entering long-term memory - Support self-improvement of coding agents - Produce explainable reasoning ("Why do we
believe this?") --- # Non-Goals The memory is **not** intended to permanently store: - AST - Source code - Tool call
history - Execution traces - LLM conversations - Complete session logs - Embeddings of everything These artifacts can
always be reconstructed. Only knowledge that cannot easily be regenerated should survive. --- # High-Level Architecture
```text Coding Session User │ ▼ +----------------------+ | Conversation | | Tool Calls | | Execution Graph | | Traces | +----------------------+ │ ▼ +--------------------------------------+ | Knowledge Consolidation Pipeline | |--------------------------------------| | Extract candidate knowledge | | Merge duplicates | | Resolve conflicts | | Validate | | Score confidence | | Apply temporal updates | +--------------------------------------+ │ ▼ +--------------------------------------+ | Persistent Temporal Knowledge Memory | +--------------------------------------+ ``` --- #
Core Ontology The ontology intentionally remains minimal. ## Fact Something believed to be true. Examples: - Repository
uses Go modules - User prefers Minimax M3 - CI runs GitHub Actions --- ## Decision An engineering decision. Examples: -
Adopt Event Sourcing - Switch to Tree-sitter - Use SQLite instead of PostgreSQL --- ## Constraint Something limiting
future work. Examples: - Pure Go only - No CGO - Docker deployment required --- ## Procedure Reusable engineering
workflow. Examples: - Release process - Build pipeline - Evaluation workflow --- ## Lesson Knowledge learned after
success or failure. Examples: - DeepSeek loses instructions in long sessions - Graph reranking performs better than LLM
reranking --- ## Observation Temporary finding. May later become: - Fact - Lesson - Decision --- ## Question Unresolved
knowledge. Examples: - Should we use Temporal? - Is graph reranking enough? Questions can later become Facts or
Decisions. --- ## Summary Compressed summary of completed work. Useful for onboarding and context loading. --- #
Relationships
```text Decision ├── based_on → Fact ├── supersedes → Decision └── introduces → Constraint Lesson ├── learned_from → Session └── prevents → Problem Procedure ├── derived_from → Lesson └── uses → Decision Question └── resolved_by → Decision ``` --- #
Temporal Model Every node has its own timeline.
```yaml created_at: valid_from: last_confirmed: superseded_at: expires_at: ``` Knowledge is **never overwritten**.
Instead: ```text Decision A ↓ Decision B ↓ Decision B supersedes Decision A ``` Complete history remains
available. --- # Provenance Every knowledge node stores its origin. ```yaml source_type: ``` Possible values: - USER -
CODE - TEST - DOCUMENT - TOOL - AGENT - SYSTEM --- # Source Priority Not all sources are equally trustworthy. | Source |
Priority | |---------|---------:| | User | 100 | | Successful Tests | 95 | | Source Code | 90 | | Build Results | 85 | |
Documentation | 80 | | Tool Output | 70 | | Agent Reasoning | 50 | | LLM Speculation | 20 | Higher-priority evidence
wins during conflicts. --- # Confidence Every knowledge node contains: ```yaml confidence: 0.97 ``` Confidence evolves
over time. Increase when: - user confirms - tests pass - repeated observations match Decrease when: - conflicts appear -
assumptions fail - user corrects the agent --- # Validation State Machine
```text Candidate ↓ Pending Validation ↓ Confirmed ↓ Deprecated ↓ Rejected ``` Rejected knowledge is never deleted. It
becomes valuable evaluation and training data. --- # Candidate Knowledge Extraction The consolidation pipeline
extracts: - Facts - Decisions - Constraints - Procedures - Lessons - Questions - Summaries Everything else is
discarded. --- # Conflict Resolution Example: ```text Fact: Database = PostgreSQL ↓ Later Fact: Database = SQLite ```
Result: ```text Fact(PostgreSQL) status = Superseded Fact(SQLite) status = Active ``` History remains intact. --- #
Consolidation Triggers ## 1. End of Session Primary consolidation. Highest-quality knowledge extraction. --- ## 2. Ask
User Boundary Whenever the workflow pauses for user input.
```text Agent ↓ Ask User ↓ Pause ↓ Extract Candidate Knowledge ↓ Wait for User ``` User feedback can immediately
validate or reject candidate knowledge. --- ## 3. Long-Running Tasks Periodic checkpoints. Possible triggers: - every N
workflow steps - after successful test suite - after completing a subtask - after merge - after deployment Checkpoint
memories remain **Pending Validation** until confirmed. --- # Knowledge Lifecycle
```text Observation ↓ Candidate ↓ Pending Validation ↓ Confirmed ↓ Fact / Decision / Lesson ↓ Deprecated ↓ Archived ``` --- #
Hallucination Protection Agent-generated knowledge is never immediately promoted.
```text Agent generates assumption ↓ Candidate ↓ Needs validation ↓ Confirmed by: • User • Tests • Code • Documentation ↓ Stored permanently ```
This prevents memory pollution. --- # Feedback Loop Incorrect memories become learning signals.
```text Wrong Decision ↓ Conflict Detected ↓ Correction Recorded ↓ Evaluation Dataset ↓ Prompt Improvement ↓ Better Agent ```
The memory system becomes a self-improving feedback mechanism. --- # Query Examples Examples of supported semantic
queries: - What decisions were made this week? - Why do we use SQLite? - Which constraints apply to this project? - What
lessons were learned from the last session? - Which decisions superseded previous ones? - Which candidate memories still
require validation? - What facts originated only from agent reasoning? - Show unresolved questions. - Show deprecated
engineering decisions. - Show all knowledge confirmed by the user. --- # Future Extensions - Multi-agent shared memory -
Team-wide organizational memory - Repository-scoped knowledge graphs - Cross-project knowledge reuse - Automatic PR
summaries - Architectural Decision Record (ADR) generation - Retrieval-Augmented Agent Memory (RAM) - Memory quality
scoring - Knowledge aging and decay - Automatic memory pruning - Reinforcement learning from corrected memories --- #
Design Principles 1. Store knowledge, not conversations. 2. Everything is temporal. 3. Nothing is deleted. 4. Provenance
is mandatory. 5. User always has the highest authority. 6. Agent reasoning is never trusted without validation. 7.
Memory continuously improves the agent. 8. The ontology remains intentionally small and extensible. 9. Long-term memory
must be explainable. 10. Knowledge should outlive individual coding sessions.
