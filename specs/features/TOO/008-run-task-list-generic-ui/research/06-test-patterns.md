# Test Patterns

**Sources:** `test/unit/builtin-commands.test.ts` and `test/helpers/fakes.ts`

## Test Helpers — `test/helpers/fakes.ts`

### FakeAgentSideConnection

A minimal stub of `AgentSideConnection` that captures all session updates:

```typescript
export class FakeAgentSideConnection {
  readonly updates: SessionUpdateMsg[] = []
  readonly permissionRequests: unknown[] = []
  nextPermissionResponse: { ... } = { outcome: { outcome: 'selected', optionId: 'allow' } }

  async sessionUpdate(msg: SessionUpdateMsg): Promise<void> {
    this.updates.push(msg)
  }

  async requestPermission(params: unknown): Promise<...> {
    this.permissionRequests.push(params)
    return this.nextPermissionResponse
  }
}
```

### FakePiRpcProcess

A stub of `PiRpcProcess` that records prompts and allows event emission:

```typescript
export class FakePiRpcProcess {
  private handlers: Array<(ev: PiRpcEvent) => void> = []
  readonly prompts: Array<{ message: string; attachments: unknown[] }> = []
  readonly extensionUiResponses: unknown[] = []
  abortCount = 0

  onEvent(handler) { this.handlers.push(handler); return () => { ... } }
  emit(ev: PiRpcEvent) { for (const h of this.handlers) h(ev) }
  async prompt(message, attachments = []) { this.prompts.push({ message, attachments }) }
  async abort() { this.abortCount += 1 }
  async sendExtensionUiResponse(response) { this.extensionUiResponses.push(response) }
  async getState() { return {} }
  async getAvailableModels() { return { models: [{ provider: 'test', id: 'model', name: 'model' }] } }
  async getMessages() { return { messages: [] } }
}
```

### asAgentConn()

Type-cast helper:

```typescript
export function asAgentConn(conn: FakeAgentSideConnection): AgentSideConnection {
  return conn as unknown as AgentSideConnection
}
```

## Built-in Command Test Pattern — `test/unit/builtin-commands.test.ts`

### Setup pattern:

```typescript
const conn = new FakeAgentSideConnection()
const proc = new FakePiRpcProcess() as any
// Override specific proc methods as needed:
proc.getState = async () => ({ steeringMode: 'one-at-a-time' })

const agent = new PiAcpAgent(asAgentConn(conn))
// Replace the session manager with a fake that returns our mock session:
;(agent as any).sessions = new FakeSessions({ sessionId: 's1', proc, fileCommands: [] }) as any
```

### FakeSessions helper (defined inline in the test file):

```typescript
class FakeSessions {
  constructor(private readonly session: any) {}
  maybeGet(_id: string) { return this.session }
  get(_id: string) { return this.session }
}
```

### Invoke prompt and assert:

```typescript
const res = await agent.prompt({
  sessionId: 's1',
  prompt: [{ type: 'text', text: '/steering' }]
} as any)

assert.equal(res.stopReason, 'end_turn')
assert.equal(proc.prompts.length, 0)  // model was NOT called
const last = conn.updates.at(-1)
assert.match((last as any).update.content.text, /Steering mode: one-at-a-time/)
```

### Key assertions:

1. **`stopReason === 'end_turn'`** — built-in commands always return `end_turn`
2. **`proc.prompts.length === 0`** — the model was never called (adapter-side handling)
3. **`conn.updates`** — check the last update has the expected `agent_message_chunk` text
4. **`conn.updates.find(u => u.update?.sessionUpdate === 'session_info_update')`** — for commands like `/name` that emit
   session info updates

## How to Test a New Built-in Command

1. Create a `FakeAgentSideConnection` and `FakePiRpcProcess`
2. Override any `proc.*` methods your command calls (e.g., `proc.getState = async () => ({ ... })`)
3. Create `PiAcpAgent` and replace `agent.sessions` with a `FakeSessions`
4. Call `agent.prompt({ sessionId, prompt: [{ type: 'text', text: '/yourcommand args' }] })`
5. Assert `stopReason`, `proc.prompts.length === 0`, and `conn.updates` content