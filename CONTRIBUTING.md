# Contributing

Please read the following document.

- https://github.com/suzuki-shunsuke/oss-contribution-guide

## Guides

- Don't use `testify` for writing tests. Use `google/go-cmp` instead.
- Don't use afero.
- Internal test files: append `_internal_test.go` for internal testing
- Logging: Use slog and [slog-error](https://github.com/suzuki-shunsuke/slog-error).
- Package layout under `pkg/`:
  - `cli/<name>` — the CLI interface and DI (flags, args, wiring).
  - `controller/<name>` — the core logic behind a command.
  - `herdr`, `mo`, `task`, ... — supporting packages that the controllers call into.
