# Sidebar Artifacts Section

## Source

User feedback on the gap that pi-go has no image-paste / drag-and-drop
flow today. ADK artifacts (`google.golang.org/adk/v2/artifact`) are the
right primitive to wire that in, but the work splits into two visible
pieces: (a) the input bar / paste handler, and (b) a sidebar section
that shows what artifacts are currently attached to the session.

This spec covers (b) only. It is the user-visible piece of a larger
image-paste feature and is being shipped first because it is
self-contained, testable, and reversible on its own.

## Idea

Add an **Artifacts** section to the right sidebar, mirroring the
existing Memory and MCP Tools patterns. The section lists every ADK
artifact stored against the current session, with a filename, an icon
(image vs. generic blob), and a humanized size. Hidden when empty.

The data source is the ADK `artifact.Service` already accessible from
the runner (`runner.Config.ArtifactService`). The renderer stays pure:
the model layer reads the artifact list once per refresh tick and
passes a `[]ArtifactEntry` into the existing `SidebarRenderInput`
struct.

## Why now

Once image-paste ships, users will paste images, switch models, walk
away, come back — and have no way to see what they attached. The
sidebar gives them an at-a-glance "what is currently in flight" view,
parallel to how Memory and MCP Tools give them a view of *other*
session state.

It is also the natural place to land click-to-view or click-to-remove
in a follow-up, without re-laying-out the sidebar.

## Non-goals

- No paste/drop UX itself (separate slice).
- No interactive selection, click-to-view, or click-to-remove.
- No inline image preview in the sidebar (lives in the input bar).
- No `user:` namespaced artifacts (cross-session) — session-scoped
  only in v1.
- No new dependency. `rsc.io/omap` is already pulled in transitively
  by ADK's `artifact.InMemoryService`.
