# AGENTS.md - mcphub

mcphub is a gateway and control plane for Model Context Protocol (MCP) servers —
"MCP Docker Kit, without Docker". You define your MCP servers **once** in
`mcphub.yaml` (or the Studio TUI), then mcphub runs a single gateway that fronts
them all and syncs the right config into every agent harness so you never
hand-edit `~/.claude.json`, `opencode.json`, and `~/.codex/config.toml` again.

- **Module**: `github.com/abdul-hamid-achik/mcphub`
- **Toolchain**: Go 1.25.5 (see `.tool-versions`)
- **Binary**: `bin/mcphub` (`task build`)
- **License**: MIT, © 2026 Abdul Hamid Achik

This file is the canonical contributor guide. `CLAUDE.md` is a short companion
that restates the essentials and the Claude-specific gotchas; when the two
disagree, **AGENTS.md wins**.

## What mcphub does (three surfaces, one config)

`mcphub.yaml` is the single source of truth. From it:

- **`mcphub mcp serve`** runs one gateway MCP stdio server. It connects to every
  *enabled* downstream server as an MCP **client**, aggregates their tools under
  namespaced names `server__tool`, and re-exposes them on ONE stdio connection.
  An agent connects to one server instead of a dozen. Every proxied call is
  timed and recorded in a local SQLite db.
- **`mcphub sync`** writes the right MCP config into every agent harness (Claude
  Code, opencode, Codex). Non-destructive merge, writes a timestamped `.bak`,
  and is **dry-run by default** (needs `--write` to apply).
- **`mcphub studio`** (alias `tui`) is a bubbletea v2 TUI to browse/toggle
  servers and view usage stats.
- **`mcphub stats`** surfaces the recorded tool-call intelligence — calls,
  errors, latency, and an estimated token cost per server & tool, so you can see
  which servers actually earn their context budget.

**Sync modes** (per agent in `mcphub.yaml`): `mode: gateway` (the default)
writes ONLY the `mcphub` server into the agent → the agent sees one MCP server,
mcphub proxies the rest → fewer tools loaded → saves tokens. `mode: direct`
writes every enabled server straight into the agent verbatim.

## Build, Test, Lint

```bash
task build      # go build -ldflags ... -o bin/mcphub ./cmd/mcphub
task test       # go test ./...
task vet        # go vet ./...
task fmt        # gofmt -w .
task tidy       # go mod tidy   (read the cellbuf caveat below first)
task cover      # tests + HTML coverage report (coverage.html)
task sqlc       # regenerate the typed DB layer (see the sqlc workflow below)
task specs      # run glyphrun end-to-end TUI/CLI specs (specs/*.yml)
task docs       # serve the VitePress docs site (docs/) locally
task docs-build # build the docs site
task run        # build + launch the Studio TUI
task serve      # build + run the gateway MCP server on stdio
task sync       # build + dry-run a sync of all agents
task dev        # rebuild on change (watch)
task install    # release build → /opt/homebrew/bin/mcphub
task version    # go version + key module versions
task clean      # remove bin/ and coverage artifacts
```

`gofmt`, `go vet`, and `go test ./...` are the gate. Run them before declaring a
change done. Single tests: `go test ./internal/<pkg> -run <Name>`.

## Architecture

`cmd/mcphub/main.go` is a thin entrypoint that calls `internal/cli.Execute()`.
Everything flows from `mcphub.yaml` (the registry) → the hub (proxy) → the
gateway MCP server, with the harness adapters and the SQLite store on the side.
Package boundaries are part of the contract — keep them clean.

| Package | Owns |
|---|---|
| `cmd/mcphub` | Entrypoint only. Defers to `internal/cli.Execute()`. |
| `internal/cli` | Cobra command tree (`root.go`) plus one file per surface: `commands.go` (init/list/enable/disable/stats/doctor), `manage.go` (add/remove/groups/use), `discover.go` (`init --from-agents` import), `sync.go`, `serve.go`, `studio.go`. Handlers stay thin; logic lives in the packages below. |
| `internal/config` | `mcphub.yaml` model, load/save, `Validate()`, path/`~` expansion, and the `expose: all\|lazy` mode (`Lazy()`). The single source of truth — the **registry** every other package reads. |
| `internal/hub` | The aggregating **proxy** ("gateway" half). Connects to each enabled downstream as an MCP client via the go-sdk, discovers tools, and `Mount`s them under `server__tool` names. `Call`/`FindTool` are the shared invoke path used by both the mounted tools (expose: all) and `mcphub_call_tool` (expose: lazy); every call records telemetry. A downstream that fails to start is recorded and skipped, never aborting the gateway. |
| `internal/mcp` | mcphub's **own** MCP stdio server (the endpoint agents point at). Registers five meta-tools (`mcphub_list_servers`, `mcphub_search_tools`, `mcphub_describe_tool`, `mcphub_call_tool`, `mcphub_stats`); in `expose: all` it also mounts every downstream tool, in `expose: lazy` only the meta-tools. |
| `internal/harness` | Agent-config **adapters**: `claude.go`, `opencode.go`, `codex.go` (`[mcp_servers.*]` TOML), `crush.go`. `harness.go` holds the `Adapter` interface (`List` + `Apply`), format-neutral `MCPServer`, the `diff` planner, and `backup`; `jsonutil.go` preserves unknown keys. `List` powers `init --from-agents`. |
| `internal/syncer` | The reconcile **engine** shared by `mcphub sync` and the Studio sync panel — `Reconcile(cfg, store, self, agents, write)` returns per-agent `Plan`s (dry-run) or applies them. `Desired` computes the gateway-vs-direct server set. |
| `internal/store` | The local intelligence layer: a single-file SQLite db (pure-Go `modernc.org/sqlite`, **no cgo**) recording every proxied call, every sync, and which servers mcphub owns in which agent. Hand-written ergonomic wrapper over the sqlc-generated `db/` package. |
| `internal/store/db` | sqlc-**generated** typed queries (`db.go`, `models.go`, `queries.sql.go`). Committed; do not hand-edit — regenerate (see below). |
| `internal/ui/studio` | The `mcphub studio` TUI on `charm.land/bubbletea/v2` + `lipgloss/v2`, with `charmbracelet/harmonica` spring-animated stat bars. Three tabs (Servers/Agents/Stats), space-toggle, and a `s` → preview → `a` apply sync panel (via `internal/syncer`). |
| `internal/version` | Build metadata (`Version`/`Commit`/`Date`) stamped via `-ldflags`. |
| `docs/` | VitePress product docs (`guide/`, `reference/`); served by `task docs`, built by `task docs-build`. |
| `specs/` | glyphrun behavioral specs (CLI + live-TUI); run by `task specs`. |

### Data flow in one paragraph

`mcphub mcp serve` → `config.Load` validates `mcphub.yaml` → `hub.New(cfg, store, logger)` → `hub.Connect` spawns/dials every enabled server concurrently (`CommandTransport` for stdio, `StreamableClientTransport`/`SSEClientTransport` for remote) and `ListTools` each → `mcp.NewServer` registers the `mcphub_*` management tools → `hub.Mount(srv)` adds each downstream tool as `server__tool` with a `forward` handler → `srv.Run(ctx, &StdioTransport{})`. Each forwarded call relays arguments unchanged, returns the downstream result verbatim, and `store.RecordCall` persists timing/bytes/error so `mcphub stats` and the Studio Stats tab can rank servers by usage and estimated token cost.

## Conventions

- **Module path** is `github.com/abdul-hamid-achik/mcphub`; internal packages
  import each other by that prefix.
- **Charm v2 lives on vanity module paths**: import `charm.land/bubbletea/v2`
  and `charm.land/lipgloss/v2` — **not** `github.com/charmbracelet/bubbletea`.
  (`github.com/charmbracelet/log` and the `charmbracelet/x/*` helpers keep their
  github paths; only bubbletea/lipgloss v2 are on `charm.land`.)
- **MCP SDK** is `github.com/modelcontextprotocol/go-sdk/mcp` (v1.6.1). The hub
  is a `mcp.Client`; the gateway is an `mcp.Server` on `StdioTransport`.
- **Errors** are returned immediately and wrapped with `fmt.Errorf("...: %w", err)`.
  Never `os.Exit` in library code — only in `main.go` / the CLI entrypoint.
- **Tests** are table-driven where it pays (config validation, harness diff/round-trip,
  store rollups). Use an in-memory store (`store.Open(":memory:")`) — never touch a
  real agent config or the user's real db in a test.
- **JSON CLI**: the persistent `--json` flag switches human output for
  machine-readable JSON wherever supported (`list`, `stats`, `doctor`). MCP tool
  handlers return a JSON text block *and* the same value as structured output
  (the `result()` helper in `internal/mcp/server.go`).
- **stdio hygiene**: in `mcp serve`, logs go to **stderr** (`log.NewWithOptions(os.Stderr, ...)`)
  so they never corrupt the stdout JSON-RPC stream. Keep it that way. Use the
  go-sdk `StdioTransport` as-is (newline-delimited JSON-RPC, not Content-Length).
- Comment exported types and non-obvious unexported functions — comments are
  part of the docs. Prefer value receivers; use pointer receivers only when the
  type owns mutable state (e.g. `*Hub`, `*Store`).

## The sqlc workflow

The typed DB layer is generated from SQL, not written by hand:

1. Edit the schema in `internal/store/migrations/*.sql` and/or the queries in
   `internal/store/queries/queries.sql`. (`sqlc.yaml`: sqlite engine,
   `emit_json_tags`, `emit_empty_slices`, `BOOLEAN → bool` override.)
2. Regenerate: `task sqlc` (runs `sqlc generate`). **Use Homebrew's sqlc at
   `/opt/homebrew/bin/sqlc`** — the asdf shim is broken. If `task sqlc` resolves
   the wrong binary, run `/opt/homebrew/bin/sqlc generate` directly.
3. The generated code in **`internal/store/db/` is committed** — review the diff
   like any other code. Never hand-edit it; change the SQL and regenerate.
4. Add the ergonomic wrapper method in `internal/store/store.go` and a test.
   Note the `asInt64` helper: SQLite `COALESCE`/`SUM`/`AVG` defeat sqlc's type
   inference (columns come back as `interface{}`), so aggregate rollups are
   coerced through it. New aggregate columns need the same treatment.

## The charm/log dependency pin (do not let tidy break it)

`go.mod` pins `github.com/charmbracelet/log v0.4.2` together with
`github.com/charmbracelet/x/cellbuf v0.0.15` so charmbracelet/log can coexist
with `charm.land/lipgloss/v2`. A naive `go mod tidy` will try to **downgrade
`x/cellbuf`**, which breaks the build. After running `task tidy`, check the
`go.mod`/`go.sum` diff: if `cellbuf` moved below `v0.0.15`, revert that hunk.
Don't bump or drop these pins casually.

## Safety: sync never destroys user config

This is load-bearing. `mcphub sync` edits files humans care about, so:

- **Dry-run by default.** `sync` prints the diff and changes nothing; only
  `--write` mutates files.
- **Backup first.** Every adapter writes `path.bak-<timestamp>` before touching
  a file (`harness.backup`).
- **Surgical merge.** Adapters only ever touch the MCP-server section
  (`mcpServers` / `mcp` / `[mcp_servers.*]`) and preserve every other key
  verbatim (`jsonutil.readJSONObject`). Pruning is scoped to entries mcphub
  previously *owned* (tracked in the `managed_entries` table), so servers a user
  added by hand are never clobbered.
- **Codex caveat**: TOML is round-tripped through a generic map, so comments and
  key ordering in `config.toml` are not preserved. The `.bak` makes this safe,
  but it is worth knowing.

Never modify the user's real agent configs (`~/.claude.json`,
`~/.config/opencode/opencode.json`, `~/.codex/config.toml`) directly, and never
exercise them from tests — point tests at temp files.

## Config & paths

`mcphub.yaml` schema (see `internal/config/config.go` and the `mcphub init`
starter):

```yaml
version: 1
servers:
  <name>: { command: <cmd>, args: [..], env: {K: V}, enabled: true, description: "...", tags: [..] }   # stdio
  <name>: { url: "https://...", transport: http|sse, enabled: false }                                  # remote
groups:
  <name>: [server, ...]
agents:
  <name>: { type: claude|opencode|codex, path: ~/path, mode: gateway|direct, disabled: false }
```

- Config path: `--config`, else `$MCPHUB_CONFIG`, else `./mcphub.yaml`, else
  `~/.config/mcphub/mcphub.yaml`.
- DB path: `--db`, else `$MCPHUB_DB`, else `~/.local/share/mcphub/mcphub.db`.

## Quick reference

```bash
./bin/mcphub --help                 # the full command surface
./bin/mcphub init                   # write a starter mcphub.yaml
./bin/mcphub list --json            # configured servers, machine-readable
./bin/mcphub enable codemap         # flip a server on (then sync to apply)
./bin/mcphub sync                   # dry-run every agent
./bin/mcphub sync claude --write    # apply just the claude agent (.bak first)
./bin/mcphub doctor --json          # diagnose config / servers / agents / store
./bin/mcphub stats --json           # local tool-call intelligence
./bin/mcphub mcp serve              # the gateway MCP stdio server
```

## Things to avoid

- Importing `github.com/charmbracelet/bubbletea` or `.../lipgloss` for v2 code —
  use the `charm.land/*/v2` vanity paths.
- Hand-editing `internal/store/db/` — change the SQL and run `task sqlc`.
- Letting `go mod tidy` downgrade `charmbracelet/x/cellbuf` below `v0.0.15`.
- Writing logs to stdout in `mcp serve` (corrupts the JSON-RPC stream).
- Mutating an agent config without a dry-run path and a `.bak`.
- Touching real `~/.claude.json` / `opencode.json` / `config.toml` from tests.
- Writing scratch `.md` reports into the repo. Repo-root Markdown is limited to
  `README`, `AGENTS`, `CLAUDE`.
