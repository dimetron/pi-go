# Initialize Response

**Source:** `src/acp/agent.ts`, lines 235–267

## How `initialize()` Works

```typescript
async initialize(params: InitializeRequest): Promise<InitializeResponse> {
  const supportedVersion = 1
  const requested = params.protocolVersion

  return {
    protocolVersion: requested === supportedVersion ? requested : supportedVersion,
    agentInfo: {
      name: pkg.name ?? 'pi-acp',
      title: 'pi ACP adapter',
      version: pkg.version ?? '0.0.0'
    },
    authMethods: getAuthMethods({
      supportsTerminalAuthMeta: (params as any)?.clientCapabilities?._meta?.['terminal-auth'] === true
    }),
    agentCapabilities: {
      loadSession: true,
      mcpCapabilities: { http: false, sse: false },
      promptCapabilities: {
        image: true,
        audio: false,
        embeddedContext: process.env.PI_ACP_ENABLE_EMBEDDED_CONTEXT === 'true'
      },
      sessionCapabilities: {
        list: {}
      }
    }
  }
}
```

## What Is Currently Advertised in `agentCapabilities`

| Field                                | Value                                               |
|--------------------------------------|-----------------------------------------------------|
| `loadSession`                        | `true`                                              |
| `mcpCapabilities.http`               | `false`                                             |
| `mcpCapabilities.sse`                | `false`                                             |
| `promptCapabilities.image`           | `true`                                              |
| `promptCapabilities.audio`           | `false`                                             |
| `promptCapabilities.embeddedContext` | env var `PI_ACP_ENABLE_EMBEDDED_CONTEXT === 'true'` |
| `sessionCapabilities.list`           | `{}`                                                |

**Not advertised:** `providers`, `nes`, `positionEncoding`, `auth.logout`, `sessionCapabilities.delete`,
`sessionCapabilities.fork`, `sessionCapabilities.resume`, `sessionCapabilities.close`,
`sessionCapabilities.additionalDirectories`.

**No plan-related capability is advertised on the agent side.** The ACP SDK `AgentCapabilities` type (lines 1320–1380)
does not have a `plan` field — plan support is a **client** capability (`ClientCapabilities.plan`).

## How Plan Support Would Be Added

### Step 1: Check client capabilities in `initialize()`

The client may send `clientCapabilities.plan` (type `PlanCapabilities | null`). The adapter should read this:

```typescript
const clientSupportsPlan = (params as any)?.clientCapabilities?.plan != null
```

Store this on the agent instance (e.g., `this.clientSupportsPlan = clientSupportsPlan`).

### Step 2: Gate plan emission on client support

Before emitting `sessionUpdate: "plan"` or `"plan_update"`, check the stored flag. The stable `"plan"` variant does NOT
require client advertisement, but `"plan_update"` / `"plan_removed"` do.

### Step 3: No agent-side capability needed

There is no `agentCapabilities.plan` field in the SDK types. The agent simply emits plan session updates; the client
opts in via `clientCapabilities.plan` for the unstable variants.

## Does the Client Send `clientCapabilities.plan`?

The SDK type `InitializeRequest.clientCapabilities` (lines 3871–3896) includes:

```typescript
plan?: PlanCapabilities | null;
```

This is **UNSTABLE/experimental**. The pi-acp adapter currently **does not read or store** this field. It would need to
be checked in `initialize()` and stored for later use.