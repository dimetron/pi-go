# Python Hello World (LSP Test)

Minimal Python project used to exercise pi-go's LSP integration with [ruff](https://github.com/astral-sh/ruff).

## Running the Code

```sh
cd hack/test/lsp/python
uv run python -m hello_world.main
```

Or directly:

```sh
uv run python src/hello_world/main.py
```

## Testing LSP

Test the Python LSP integration:

```sh
# Test diagnostics
go run ./cmd/pi --mode json "Use lsp-diagnostics on hack/test/lsp/python/src/hello_world/main.py"

# Test code actions
go run ./cmd/pi --mode json "Use lsp-code-action on hack/test/lsp/python/src/hello_world/main.py at line 10"
```

## Notes

Ruff's LSP server supports a subset of LSP features:

- **Diagnostics** (errors, warnings from ruff rules) ✓
- **Code actions** (quick fixes, auto-fixes) ✓
- **Go to definition** ✓
- **Find references** ✓
- Hover/type info ✗ (not supported by ruff)
- Document symbols ✗ (not supported by ruff)
- Workspace symbols ✗ (not supported by ruff)

Ruff provides fast diagnostics using its own rule set rather than type checking.
For type checking, consider pyright or pylsp as an alternative LSP server.