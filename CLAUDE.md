# CLAUDE.md

**Source of truth: `AGENTS.md`. Read it first.** This file adds Claude-specific
orientation and the handful of things that are easy to get wrong. When the two
disagree, AGENTS.md wins.

## What mcphub is

A gateway + control plane for MCP servers — "MCP Docker Kit, without Docker".
Define servers once in `mcphub.yaml`; then `mcphub mcp serve` fronts them all on
one stdio connection (tools namespaced `server__tool`), and `mcphub sync` writes
the right config into each agent harness. Every proxied call is logged to a local
SQLite db so `mcphub stats` shows which servers earn their context budget.

- **Module**: `github.com/abdul-hamid-achik/mcphub` · **Go 1.25.5** · MIT, © 2026 Abdul Hamid Achik
- CLI surface: `./bin/mcphub --help` (init, list/ls, enable/disable, sync,
  studio/tui, stats, doctor, mcp serve; persistent `--config`/`--db`/`--json`).

## The gate

`task build` · `task test` (or `go test ./...`) · `task vet` · `task fmt`. Run
them before declaring a change done. Single test:
`go test ./internal/<pkg> -run <Name>`.

## Architecture in one line

`cmd/mcphub` → `internal/cli` (thin Cobra handlers) reads `internal/config`
(`mcphub.yaml`, the registry) → `internal/hub` dials each enabled server as a
go-sdk MCP **client** and `Mount`s their tools as `server__tool` onto
`internal/mcp` (mcphub's own gateway MCP server) → `internal/harness` adapters
sync that into 11 agent configs (Claude, opencode, Codex, Crush, Forge, Hermes,
Copilot CLI, Qwen Code, Gemini CLI, Kilo Code, Kimi Code CLI) → `internal/store`
records telemetry and which servers mcphub owns. TUI: `internal/ui/studio`
(charm.land v2). Keep these boundaries clean.

## Gotchas (learned the hard way)

- **Charm v2 is on vanity module paths**: import `charm.land/bubbletea/v2` and
  `charm.land/lipgloss/v2`, **not** `github.com/charmbracelet/...`. (Only
  bubbletea/lipgloss v2 move; `charmbracelet/log` and `charmbracelet/x/*` keep
  their github paths.)
- **Don't let `go mod tidy` downgrade `charmbracelet/x/cellbuf`**. It is pinned
  at `v0.0.15` alongside `charmbracelet/log v0.4.2` so log coexists with
  lipgloss/v2. After `task tidy`, inspect the `go.mod`/`go.sum` diff and revert
  any cellbuf downgrade.
- **sqlc is brew, not asdf.** The DB layer in `internal/store/db/` is generated:
  edit `internal/store/{migrations,queries}` then `task sqlc`. The asdf shim is
  broken — use `/opt/homebrew/bin/sqlc`. The generated code is committed; never
  hand-edit it. Aggregate columns come back as `interface{}` (SQLite
  COALESCE/SUM/AVG defeat sqlc inference) — route them through `asInt64`.
- **stdio framing**: in `mcp serve`, logs go to **stderr**; stdout is the
  JSON-RPC stream. Use the go-sdk `StdioTransport` as-is (newline-delimited, not
  Content-Length). Never `fmt.Println` to stdout from the gateway.
- **`sync` is sacred.** It is dry-run by default, writes a timestamped `.bak`
  before any write, only touches the MCP-server section of each file, and prunes
  only entries mcphub previously *owned* (the `managed_entries` table) so
  hand-added servers survive. Never touch real agent configs
  (`~/.claude.json`, `~/.copilot/mcp-config.json`, `~/.qwen/settings.json`,
  `~/.codex/config.toml`, etc.) directly or from tests — use temp files (and
  `store.Open(":memory:")`).

## Conventions

- Errors wrapped with `fmt.Errorf("...: %w", err)`; no `os.Exit` in library code.
- Table-driven tests; `--json` for machine-readable CLI output.
- Comment exported types. Value receivers unless the type owns mutable state.
- Don't write scratch `.md` into the repo — repo-root Markdown is limited to
  `README`, `AGENTS`, `CLAUDE`.
