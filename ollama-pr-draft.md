# Drop the mailru/easyjson transitive dependency

## Summary

`internal/orderedmap` wraps `github.com/wk8/go-ordered-map/v2`, whose `json.go`
imports `github.com/mailru/easyjson/jwriter`. easyjson is a VK-affiliated
library (maintainers based in Russia, affiliated with VK Group) that has been
flagged as a supply-chain risk — see the Hunted Labs report
["The Russian Open Source Project That We Can't Live Without"](https://huntedlabs.com/the-russian-open-source-project-that-we-cant-live-without/)
and the tracking issues in other projects:

- lima-vm/lima#3527 — "Remove indirect dependency on github.com/mailru/easyjson"
- invopop/jsonschema#182 — "Consider removing easyjson dependency due to security risks"
- getkin/kin-openapi#1192 — "Consider removing easyjson dependency due to sanction concerns"

This PR removes easyjson from the build graph by switching the ordered-map
dependency to the API-compatible fork that dropped it.

## Change

Swap the ordered-map dependency in `internal/orderedmap/orderedmap.go`:

```go
-	orderedmap "github.com/wk8/go-ordered-map/v2"
+	orderedmap "github.com/pb33f/ordered-map/v2"
```

and update `go.mod`:

```
-	github.com/wk8/go-ordered-map/v2 v2.1.8
+	github.com/pb33f/ordered-map/v2 v2.3.1
```

then `go mod tidy` drops `github.com/mailru/easyjson` (and `gopkg.in/yaml.v3`)
from `go.sum`.

## Why this fork

`github.com/pb33f/ordered-map/v2` is a fork of `wk8/go-ordered-map` that
removed the easyjson import from `json.go` (it now uses only stdlib
`encoding/json` plus `github.com/buger/jsonparser`). It is already the
dependency of choice in the ecosystem for this exact reason — `invopop/jsonschema`
v0.14.0 and lima both switched to it (lima-vm/lima#4916).

The public API used by `internal/orderedmap` is unchanged — `New`, `Get`,
`Set`, `Len`, `Oldest`, `Next`, `ToMap`, `MarshalJSON`, `UnmarshalJSON` all
have identical signatures — so the wrapper needs no code changes.

## Verification

- `go build ./...` passes
- `go test ./api/...` passes
- `go mod tidy` removes `github.com/mailru/easyjson` from `go.sum`
- `go list -deps` no longer contains `github.com/mailru/easyjson`
