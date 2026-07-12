# ACP SDK Plan Types

**Source:** `node_modules/@agentclientprotocol/sdk/dist/schema/types.gen.d.ts`
**SDK version:** `^0.26.0`

## Plan-Related Types

### PlanEntry (lines 3365–3389)

```typescript
export type PlanEntry = {
  content: string;           // Human-readable description of the task
  priority: PlanEntryPriority;
  status: PlanEntryStatus;
  _meta?: { [key: string]: unknown } | null;
};
```

### PlanEntryPriority (line 3397)

```typescript
export type PlanEntryPriority = "high" | "medium" | "low";
```

### PlanEntryStatus (line 3404)

```typescript
export type PlanEntryStatus = "pending" | "in_progress" | "completed";
```

### Plan (lines 3414–3432) — STABLE (not UNSTABLE)

```typescript
export type Plan = {
  entries: Array<PlanEntry>;
  _meta?: { [key: string]: unknown } | null;
};
```

Used with `sessionUpdate: "plan"`. The client replaces the entire plan with each update.

### PlanId (line 3458) — UNSTABLE/experimental

```typescript
export type PlanId = string;
```

### PlanItems (lines 3468–3490) — UNSTABLE

```typescript
export type PlanItems = {
  id: PlanId;
  entries: Array<PlanEntry>;
  _meta?: { [key: string]: unknown } | null;
};
```

### PlanFile (lines 3500–3519) — UNSTABLE

```typescript
export type PlanFile = {
  id: PlanId;
  uri: string;
  _meta?: { [key: string]: unknown } | null;
};
```

### PlanMarkdown (lines 3529–3548) — UNSTABLE

```typescript
export type PlanMarkdown = {
  id: PlanId;
  content: string;
  _meta?: { [key: string]: unknown } | null;
};
```

### PlanUpdateContent (lines 3442–3448) — UNSTABLE

Discriminated union:

```typescript
export type PlanUpdateContent =
  | (PlanItems & { type: "items" })
  | (PlanFile & { type: "file" })
  | (PlanMarkdown & { type: "markdown" });
```

### PlanUpdate (lines 3558–3573) — UNSTABLE

```typescript
export type PlanUpdate = {
  plan: PlanUpdateContent;
  _meta?: { [key: string]: unknown } | null;
};
```

Used with `sessionUpdate: "plan_update"`.

### PlanRemoved (lines 3583–3598) — UNSTABLE

```typescript
export type PlanRemoved = {
  id: PlanId;
  _meta?: { [key: string]: unknown } | null;
};
```

Used with `sessionUpdate: "plan_removed"`.

### PlanCapabilities (lines 4016–4027) — UNSTABLE

Client-side capability. Advertised in `ClientCapabilities.plan`:

```typescript
export type PlanCapabilities = {
  _meta?: { [key: string]: unknown } | null;
};
```

Supplying `{}` means the client can receive both `plan_update` and `plan_removed` session updates.

## How Plan Types Are Used in SessionUpdate

`SessionUpdate` (lines 3247–3273) is a discriminated union. Plan-related variants:

```typescript
export type SessionUpdate =
  | (Plan & { sessionUpdate: "plan" })               // STABLE: full plan with entries
  | (PlanUpdate & { sessionUpdate: "plan_update" })   // UNSTABLE: update plan by ID
  | (PlanRemoved & { sessionUpdate: "plan_removed" }) // UNSTABLE: remove plan by ID
  | ... // other variants (agent_message_chunk, tool_call, etc.)
```

### ClientCapabilities.plan (lines 3920–3927)

```typescript
// Inside ClientCapabilities:
plan?: PlanCapabilities | null;
```

> Whether the client supports `plan_update` and `plan_removed` session updates.
> Optional. Omitted means the client does not advertise support.
> Supplying `{}` means the client can receive both update types.

**Key insight:** There are TWO plan mechanisms:

1. **Stable `plan`** — simple `Plan` with `entries: PlanEntry[]`, sent as `sessionUpdate: "plan"`. No plan ID.
2. **Unstable `plan_update` / `plan_removed`** — plan identified by `PlanId`, supports items/file/markdown content
   types. Requires `ClientCapabilities.plan` to be advertised.