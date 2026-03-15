# Repository Guidelines

## Project Structure & Module Organization
`cmd/gengowatcher` contains the CLI entrypoint. Public contracts live in `pkg/gengo`, while implementation details stay under `internal/`: `app` orchestrates monitors, `monitor/rss` and `monitor/websocket` ingest jobs, `pipeline` handles first-seen routing and telemetry, `reaction` dispatches actions, `telemetry/sqlite` persists metrics, and `ui` contains the Bubble Tea TUI. Keep design notes in `ARCHITECTURE.md`, roadmap material in `go-rewrite-plan.md`, and phase plans under `docs/plans/`. Tests are package-local `*_test.go` files, with extra fixtures under `tests/fixtures`.

## Build, Test, and Development Commands
Use standard Go tooling:

- `go build ./cmd/gengowatcher` builds the CLI binary.
- `go run ./cmd/gengowatcher -c config.toml` runs locally with an explicit config file.
- `go test ./...` runs the full regression suite.
- `go test ./internal/monitor/rss ./internal/monitor/websocket -v` focuses on monitor behavior.
- `go test ./internal/config ./internal/pipeline ./internal/ui -v` is useful after config, routing, or TUI changes.

## Coding Style & Naming Conventions
Format all Go code with `gofmt -w` before submitting. Follow idiomatic Go: tabs for indentation, exported names in `CamelCase`, unexported helpers in `camelCase`, and short package names (`ui`, `config`, `dedupe`). Keep public API changes confined to `pkg/gengo`; prefer `internal/` for new implementation details. Favor table-driven tests and small, package-scoped helpers over shared global test utilities.

## Testing Guidelines
Add tests next to the code they cover and name them `Test<Type>_<Behavior>`. Cover both happy paths and resilience cases, especially around reconnects, backoff, dedupe, shutdown, and non-blocking UI/event flow. When changing monitor or pipeline behavior, run targeted package tests first, then finish with `go test ./...`.

## Commit & Pull Request Guidelines
Recent history uses short Conventional Commit-style subjects such as `fix: ...`, `chore/docs`, and `chore(deps): ...`; keep that pattern and write in the imperative. Pull requests should summarize the behavior change, list the commands you ran, and link the relevant issue or plan document. Include screenshots or terminal captures when modifying the TUI, and call out config or environment-variable changes explicitly.

## Configuration & Security Tips
Configuration loads from `config.toml` plus `GENGO_*` environment overrides via Viper. Keep secrets such as `GENGO_USER_ID`, `GENGO_USER_SESSION`, and `GENGO_USER_KEY` out of tracked files; use environment variables only. If you enable RSS, keep `watcher.check_interval` at or above the validated minimum of `31s`.
