# Changelog

All notable changes to mcphub are documented here. The format is loosely based
on [Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/) once it tags releases.

## [0.18.0] - 2026-07-19

### Added

- **Per-agent advertisement budgets.** Gateway agents can override global lazy
  pins with `pin` (including `pin: []`) and cap complete directly advertised
  downstream definitions with `tool_schema_budget`. Hidden in-scope tools
  remain discoverable and callable through the eight management tools.
- **Live downstream catalog refresh.** MCP `tools/list_changed` notifications
  now refresh the affected catalog and mounted tool surface without restarting
  the gateway. Reconnect restores future calls but never replays a failed call
  whose outcome may already include a mutation.

### Changed

- Resolver instructions now require a confident, unambiguous recommendation
  before automatic invocation and direct ambiguous results toward comparison,
  a narrower search, or user direction.
- Scoped doctor probes connect only the requested server and return a non-zero
  status after rendering their human or JSON report when registration, PATH,
  or MCP initialization fails.

### Fixed

- Protected downstream session, error, and tool-catalog state with coherent
  snapshots, removing races between transport invalidation, reconnects,
  catalog refresh, doctor, and management-tool reads.
- Dynamic mounts now remove stale definitions before adding replacements, so a
  concurrent `tools/list` cannot observe a temporary union that exceeds the
  selected agent scope or schema budget.

### Security

- TinyVault control credentials are stripped from unrelated stdio children.
  Startup stderr and `tvault://` header failures are bounded, credential
  redacted, and reduced to closed diagnostic classes; decrypted header output
  is size- and timeout-bounded.

## [0.17.0] - 2026-07-16

### Fixed

- **SIGTERM shutdown is bounded with busy detached calls.** `Hub.Close` now
  signals a shutdown context before tearing down sessions: in-flight detached
  calls are cancelled instead of running out their full timeouts (up to 30m),
  and the transport-failure path no longer respawns a downstream child while
  the gateway is closing — including a TOCTOU window in `ReconnectOne` where
  a reconnect racing `Close` could install and leak a fresh session.

### Documentation

- CHANGELOG sections backfilled for v0.7.0 through v0.13.0; `mcphub remove`
  and the persistent-flags list in the README corrected; CI actions pinned to
  verified commit SHAs and the release build aligned to Go 1.25.5 with -race.

## [0.16.3] - 2026-07-16

### Fixed

- **Sync detail no longer leaks secrets.** The v0.16.2 field-level dry-run
  detail printed argument values and full URLs verbatim: `--token=xyz` and
  query-string API keys reached the report (panel review 2026-07-16, same
  day). Argument values behind secret-named flags are now masked
  (`--token=***`, `--api-key ***`), URLs are stripped of query strings,
  fragments, and userinfo, control characters are removed so a hostile value
  cannot inject report lines or terminal escapes, and truncation happens on a
  rune boundary so the detail is always valid UTF-8.

### Documentation

- The sync guide documents the field-level `update` detail and its redaction
  rules; the meta-tools reference documents stutter-collapsed alias
  resolution (v0.16.1).

## [0.16.2] - 2026-07-16

### Added

- **Sync dry runs explain updates field by field.** A planned `update` now
  prints what actually differs (`command "mcphub" → "/opt/homebrew/bin/mcphub";
  args [] → [mcp serve]`) so a pending change is reviewable without hand-diffing
  the harness file. Env VALUES never appear — harness env blocks commonly hold
  credentials — only which keys were added/removed/changed. The detail is also
  in the JSON plan output (`changes[].detail`).

### Fixed

- **Gateway command paths no longer churn with the launch location.** Sync
  recorded `os.Executable()` verbatim as the harness `command`, so running
  `sync --write` from a Caskroom-versioned path (or any duplicate install)
  would repoint every harness config at that unstable location. When the
  executable's basename resolves on PATH to the same underlying file, the
  stable PATH name is recorded instead; a genuinely different binary (a dev
  build outside PATH) keeps its explicit path.

## [0.16.1] - 2026-07-16

### Fixed

- **Stutter-collapsed tool aliases resolve.** Downstream servers commonly
  self-prefix their tool names (hitspec's search tool is `hitspec_search_web`),
  so the gateway-namespaced form stutters (`hitspec__hitspec_search_web`) and
  callers reasonably tried `hitspec__search_web` or
  `{server: "hitspec", tool: "search_web"}` — and got a bare "tool not found".
  `mcphub_describe_tool` and `mcphub_call_tool` (sync and detached) now resolve
  the collapsed alias to the canonical downstream name whenever the bare name
  matches nothing; exact names always win, so no downstream tool can be
  shadowed. Receipts, telemetry, and scope checks all use the canonical name.

## [0.16.0] - 2026-07-15

### Added

- **Detached long-running calls.** `mcphub_call_tool` accepts `detach: true` for
  downstream tools that can outlive the client's tool-call timeout (repository
  indexing, large scans): the gateway starts the call in the background and
  returns an `accepted` receipt with a `callId` immediately. The new
  `mcphub_poll_result` meta-tool reports `pending`/`failed`/`unknown` and, once
  the call completes, hands back the tool result exactly as a synchronous call
  would have (oversized results as stored-result receipts). The in-memory
  registry is bounded (8 in flight, 128 retained for 24 hours) and does not
  survive a gateway restart. An optional `timeout_ms` argument bounds any call,
  clamped by the new `call_timeout` config key (default 30m).

## [0.15.0] - 2026-07-13

### Added

- **Context-aware lazy discovery.** Servers can declare bounded `use_when`
  routing hints, and `mcphub_resolve_tool` / `mcphub_search_tools` now rank
  full natural-language task context across tool metadata (including bounded
  top-level input field names), server descriptions, tags, and those hints.
  Lazy-mode instructions expose a compact,
  scope-aware capability summary so compatible harnesses can route work to
  unpinned tools as a task moves from research to planning, implementation, and
  verification. Search results are bounded by `max_hits` (20 by default).
- **Per-tool contextual hints.** A server may add bounded `tool_use_when`
  phrases when several tools share server vocabulary but serve different
  activities, such as video timeline analysis versus image inspection.
- **First-class Bob integration.** The documentation now includes a complete
  trusted-local and least-privilege registration guide for Bob's six typed
  repository tools, lazy-mode pins, local-agent routing, and local-only stats.

### Changed

- Contextual resolver responses now expose a versioned confidence decision and
  content-addressed catalog revision. Weak coverage and close scores are
  ambiguous instead of being presented as clear routes.
- Agent server scopes are applied before downstream connection, so excluded
  commands, network connections, and secret resolution remain inactive.
- A downstream transport failure reconnects the server for future work but no
  longer replays an outcome-unknown tool call.
- The starter configuration includes the supported `local-agent` harness, so
  `mcphub mcp serve --agent local-agent` works after a fresh `mcphub init`.
- Namespaced downstream tools retain their complete MCP metadata, including
  titles, input/output schemas, annotations, icons, and `_meta`; MCPHub changes
  only the protocol name and description prefix.
- Documentation now uses the current pinned VitePress 2 alpha toolchain, which
  removes the vulnerable Vite/esbuild versions from the locked dependency graph.

## [0.14.0] - 2026-07-10

### Added

- **Bounded, lossless gateway results.** Every mounted, pinned, and lazy success
  now passes through one `Hub.Call` finalizer. Complete results over `response_budget` are stored
  in SQLite for 24 hours under an opaque call ID, then recovered byte-for-byte through the new
  `mcphub_get_result(callId,cursor)` management tool. Pages are bounded base64 JSON, scope-checked,
  and restart-safe; store failures fail open to the full result instead of losing data. Small
  results, `verbatim: true`, and `response_budget: "0"` remain exact pass-through.

## [0.13.0] - 2026-07-10

### Added

- **`sync --resume <planId>` / `sync --rollback <planId>`.** `--resume` extracts
  the agent name from the plan ID and re-syncs just that agent with `--write`
  forced on, re-applying the current desired config. `--rollback` finds the most
  recent `.bak` backup for that agent's config path and restores it, undoing the
  last sync. Both parse the `plan_<timestamp>_<agent>` plan IDs generated since
  v0.10.0.

## [0.12.0] - 2026-07-10

### Added

- **Custom headers and `tvault://` secret refs for remote servers.** A remote
  (`url`) server may declare a `headers` map, injected into every request via a
  wrapping `http.RoundTripper` — enabling bearer-token authentication for
  services like the Obsidian Local REST API plugin. Header values shaped
  `tvault://<project>/<key>` are resolved at gateway connect time by shelling
  out to `tvault get`, keeping secrets out of `mcphub.yaml` entirely (the
  existing `vault:` field only covered stdio servers). TLS verification is
  auto-skipped for localhost HTTPS endpoints (the common self-signed-cert
  pattern, safe on loopback), and validation rejects headers on stdio servers.

## [0.11.0] - 2026-07-10

### Added

- **`mcphub_resolve_tool` meta-tool.** Collapses the search → describe → call
  pattern into one call: it takes a natural-language query, ranks catalog
  matches (exact name > name substring > description), and returns one
  recommendation with `required_fields` extracted from the tool's JSON Schema,
  an `argument_template` skeleton, up to N ranked `alternatives`, and an
  `ambiguous` flag when the top two rank equally — so a lazy-mode agent can
  find and call a tool in one step instead of three.

## [0.10.0] - 2026-07-10

### Added

- **Sync plan IDs.** Every sync now generates a durable plan ID
  (`plan_<timestamp>_<agent>`), carried on each agent's result and included in
  the JSON output, so a run can be referenced for programmatic resume/rollback.

### Fixed

- **Config and ownership bookkeeping stay consistent.** When a config write
  succeeded but recording ownership (`SetManaged`) failed, sync previously left
  the agent config mutated with stale ownership. The backup taken before the
  apply is now automatically restored in that case, and the error message notes
  the restore.

## [0.9.0] - 2026-07-10

### Added

- **Response budget + truncation honesty.** `mcphub_call_tool` caps downstream
  results that exceed a configurable budget (default 32KB): text content blocks
  are truncated and a notice is appended so the agent knows the result was
  capped; non-text content passes through. New config: `response_budget` (e.g.
  `"32KB"`, `"1MB"`, `"0"` for unlimited) and `verbatim: true` to opt out.
  (Truncation was superseded by lossless result spooling in v0.14.0.)

## [0.8.0] - 2026-07-10

### Added

- **Immediate reconnect on transport failure.** `Hub.Call` now detects
  transport/protocol failures, invalidates the stale downstream session, and
  reconnects immediately (`ReconnectOne`) instead of waiting for the 30s
  background watcher; on a successful reconnect the call was retried once. The
  existing watcher continues to handle downstreams that fail between calls.
  (v0.15.0 later removed the automatic replay: an outcome-unknown call is
  reconnected for future work but never retried.)

## [0.7.0] - 2026-07-06

### Added

- **`add --enabled`** — accepted as a no-op alias for the default (mutually
  exclusive with `--disabled`), so ecosystem onboarding docs that bake in
  `mcphub add <name> <cmd> --enabled` work instead of erroring at step one.
- **`doctor --server <name>` / `status --server <name>`** — a cheap
  single-server "am I wired into the gateway?" view: registration, enabled
  state, PATH availability, the agents that route to it, and proxied-call
  count. `doctor --server <name> --probe` also performs the real handshake and
  fills `handshake_ok` + `tool_count`; with `--json` the scoped object matches
  the contract downstream tools (e.g. Cortex) consume.

## [0.6.0] - 2026-07-06

### Added

 - **Per-agent server & tool routing.** Each agent may now declare a `servers`
   allowlist (which enabled downstream servers it may reach) and/or a `tools`
   allowlist (which `server__tool` names a gateway-mode agent may call). In
   direct mode only the listed servers are written; in gateway mode the
   spawned `mcphub mcp serve --agent <name>` advertises only the allowed
   subset and refuses out-of-scope calls through `mcphub_call_tool` /
   `mcphub_describe_tool` / `mcphub_search_tools` / `mcphub_list_servers`.
   `doctor` reports each agent's scope and warns about listed-but-disabled
   servers; the Studio Agents tab renders the routing config. An omitted
   `servers`/`tools` is unscoped (sees everything — the default); an explicit
   empty list (`servers: []` / `tools: []`) means **none** (a deliberately
   minimal agent), kept distinct from "all" by storing the allowlists as
   pointers. Curation, not a security isolation boundary.

### Fixed

 - **`specs/offload.yml`** — the end-to-end spec for `mcphub offload` now models
   the real register-then-offload flow (a direct-mode `sync --write` records the
   server as managed, the gateway is then added by hand), so the spec passes
   instead of reporting "nothing to offload" against a hand-seeded agent file.

## [0.5.0] - 2026-07-03

### Added

 - **Five new agent adapters** — Copilot CLI, Qwen Code, Gemini CLI, Kilo Code,
   and Kimi Code CLI, bringing the total to eleven supported harnesses.
   `mcphub sync`, `init --from-agents`, and the Studio TUI cover all eleven;
   `mcphub agents` lists every supported type with its configured / available /
   not-installed status.
 - **`mcphub agents`** command — list all supported harness types and their
   status (configured / available / not installed).

### Fixed

 - **Enabled-flag round-trip** — a managed server with an explicit
   `enabled: false` survives a sibling change without being reset.

## [0.4.0] - 2026-06-29

### Added

 - **`stats --server <name>`** — filter the per-tool breakdown to one server.
 - **Configurable connect timeout** — a `connect_timeout` field in `mcphub.yaml`
   (or `--connect-timeout` flag on `mcp serve` / `doctor --probe`) controls how
   long the gateway waits for a downstream to start (default 30s).
 - **Downstream reconnection** — the gateway now health-checks downstreams and
   reconnects failed/stale ones automatically during `mcp serve`, so a crashed
   server self-heals without restarting the agent.
 - **Agent type validation** — `config.Validate()` now rejects unknown agent
   types at load/save time, catching typos like `type: cluade` before they
   surface at `sync`.
 - Tests for `transportFor` (4 branches + vault wrapping), `parseEnv` (7
   cases), MCP meta-tool handlers (list/search/describe/call/stats), and a
   starter-config drift guard (asserts the YAML starter and `Starter()` match).

### Fixed

 - **Client version** — the MCP client implementation now reports the actual
   build version instead of a hardcoded `"0.1.0"`.
 - **`SetManaged` atomicity** — the clear-and-reinsert is now wrapped in a SQL
   transaction, so a mid-loop failure can't partially wipe the managed-entries
   table.
 - **`handleCallTool` error semantics** — infrastructure errors (unknown
   server, not connected, marshal failure) now return proper MCP protocol
   errors instead of a successful result with an `{"error":...}` body.
 - **`offload` safety** — now uses the store's managed-entries as the owned set
   (so a user's hand-added entry sharing a proxied name is never clobbered) and
   updates the store after a successful write.
 - **File permissions** — `config.Save` preserves the existing file's mode
   (defaulting to `0o600` for new configs) and `backup` preserves the source
   file's mode, instead of hardcoding `0o644`.
 - **SQLite pool** — `SetMaxOpenConns(1)` is now set for all SQLite databases,
   not just `:memory:`, preventing "database is locked" errors under concurrent
   gateway writes.
 - **Tool existence pre-check** — `Hub.Call` now checks the tool exists on the
   downstream before forwarding, giving a clear "tool not found" error instead
   of a round-trip to the downstream.

### Changed

 - **JSON adapter dedup** — the four JSON harness adapters (claude, opencode,
   crush, forge) now share a single `jsonAdapter` implementation, eliminating
   ~200 lines of duplicated `List`/`Apply` boilerplate. Harness coverage rose
   from 77.8% to 81.8%.
 - **`transportFor` simplified** — removed the dead `error` return (it was
   always nil); the function now returns just `mcp.Transport`.

## [0.3.0] - 2026-06-28

### Added

- **`mcphub offload`** — removes the gateway-proxied servers from your agents so
  each relies purely on the `mcphub` gateway (the "offload" half of "register and
  offload"). Dry-run by default; `--write` applies with a `.bak`. Leaves the
  gateway and any non-proxied / agent-internal servers untouched.
- **Server & wildcard pins.** A `pin` entry can now be a whole server
  (`codemap` — all its tools), a wildcard (`codemap__*`), or one tool. Pinned
  tools stay directly callable in `expose: lazy`, so agents auto-invoke them
  instead of going through `mcphub_search_tools` first.
- **`mcphub pin` / `unpin` CLI** (and `p` in Studio) to manage pins without
  editing YAML. `mcphub pin --top N` auto-pins your N most-called tools from the
  local intelligence store.
- Stronger lazy-mode gateway instructions, nudging models to discover and call
  tools proactively.

### Fixed

- Pin validation now rejects forms `PinMatches` can't honor (partial wildcards,
  trailing `__`); Studio and `unpin` resolve pins by server (so a bare name
  clears its wildcard/exact pins too); `pin --top` skips stale usage history;
  `offload` never removes the reserved `mcphub` gateway and no longer aborts the
  whole run on one agent's error. (`mcphub` is now a reserved server name.)

## [0.2.0] - 2026-06-28

### Added

- **Config in YAML, TOML, or JSON.** The config file format is chosen from its
  extension (`mcphub.yaml` / `.toml` / `.json`); mcphub reads and writes all
  three, so `enable`/`disable`/`add`/Studio round-trip in whatever format you
  picked. `mcphub init --format yaml|toml|json`.
- **Markdown reports.** `mcphub status --markdown` and `mcphub stats --markdown`
  emit clean Markdown (headings + tables) for pasting into notes or issues.
- **Richer starter config.** `mcphub init` now seeds `monitor` and `cairntrace`
  servers and wires up all six agents (claude/opencode/codex/crush/forge/hermes)
  with XDG-aware paths, plus an `ops` group.

## [0.1.0] - 2026-06-28

First release. `brew install abdul-hamid-achik/tap/mcphub`.

### Added

- **Gateway** (`mcphub mcp serve`) — connects to every enabled downstream MCP
  server as a client, aggregates their tools under `server__tool` names, and
  re-exposes them on a single stdio connection. Each proxied call is recorded.
- **Lazy exposure** (`expose: lazy`) — the gateway advertises only five
  meta-tools (`mcphub_list_servers`, `mcphub_search_tools`,
  `mcphub_describe_tool`, `mcphub_call_tool`, `mcphub_stats`) and serves the
  real tools on demand, so the agent's context cost stays tiny regardless of
  fleet size.
- **Sync** (`mcphub sync`) — reconciles `mcphub.yaml` into agent harness configs
  (Claude Code, opencode, Codex, Crush). Non-destructive merge, timestamped
  `.bak`, dry-run by default. Gateway and direct modes per agent.
- **Bootstrap** — `mcphub init --from-agents` imports the servers your harnesses
  already declare; `add` / `remove` / `enable` / `disable` / `groups` / `use`
  manage `mcphub.yaml` from the CLI.
- **Studio TUI** (`mcphub studio`) — bubbletea v2 + lipgloss v2 with harmonica
  spring-animated stat bars. Three tabs (Servers / Agents / Stats), space to
  toggle, `s` to preview-and-apply a sync, `x` to flip exposure.
- **Intelligence** — a local SQLite store (sqlc, pure-Go `modernc.org/sqlite`)
  recording every proxied call. `mcphub stats` (`--tools`, `--recent N`) and
  `mcphub status` (per-agent sync drift + flags enabled-but-unused servers).
- **Secrets via tvault** — a server may set `vault: <project>`; mcphub launches
  it through `tvault run` so the project's secrets are injected as env vars and
  never live in `mcphub.yaml`.
- **`doctor --probe`** — actually spawns each enabled server, performs the MCP
  handshake, and reports its tool count (a real connectivity check).
- **Pinned tools in lazy mode** — a top-level `pin: [server__tool]` list keeps
  your most-used tools directly mounted even under `expose: lazy`, so the common
  path skips the search/describe round-trip while everything else stays lazy.
- **Forge and Hermes harness adapters** — mcphub now syncs to all six harnesses:
  Claude Code, opencode, Codex, Crush, Forge (`.mcp.json`), and Hermes
  (`~/.hermes/config.yaml`). `init --from-agents` imports from all of them.
- **Time-windowed stats** — `mcphub stats --since 24h` (accepts `90m`, `7d`,
  etc.) scopes the totals and per-server/per-tool breakdowns to a recent
  lookback window, surfacing which servers earn their context budget *lately*.
- Documentation: README, AGENTS.md / CLAUDE.md, a VitePress docs site under
  `docs/`, and nine glyphrun end-to-end specs (CLI + live TUI) under `specs/`.
- **Distribution** — a GoReleaser config (darwin/linux × amd64/arm64,
  CGO-free static binaries with stamped version/commit/date) that publishes a
  Homebrew cask to `abdul-hamid-achik/homebrew-tap`, plus a tag-triggered release
  workflow. `brew install abdul-hamid-achik/tap/mcphub` once tagged.

### Fixed

- **Sync no longer drops user-added keys.** Writing a config now only rewrites
  the entries the diff actually changes, and overlays the modeled fields onto
  each entry instead of replacing it — so custom headers/timeouts (and an
  explicit `enabled: false`) on a managed server survive a sibling change.
- **Remote servers are idempotent.** A second `sync` of an unchanged remote
  (`url`) server is now a no-op instead of churning the file and a fresh `.bak`
  every run (the transport round-trip is normalized per adapter).
- **Telemetry no longer loses the interesting tail.** Cancelled/timed-out calls
  are recorded under a detached context, and a tool that returns `IsError` now
  counts as an error in `stats`/`status` instead of a success.
- Atomic, validated `config.Save` (temp-file + rename; rejects invalid configs
  before writing); collision-proof `.bak` naming; `mcphub --version` now shows
  the stamped commit/date; `mcphub list` shows tags and a vault marker;
  `splitNamespaced` handles the combined `server__tool` form robustly.
- **The VitePress docs site now builds.** The config was `.vitepress/config.ts`
  without `"type": "module"`, which fails with an ESM/require error; renamed to
  `config.mts` and set the package type. `npm run docs:build` (and `task
  docs-build`) renders all pages and validates every internal link. The site is
  also deploy-ready (`docs/vercel.json`, VitePress framework preset).
[Unreleased]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.18.0...HEAD
[0.18.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.16.3...v0.17.0
[0.16.3]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.16.2...v0.16.3
[0.16.2]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.16.1...v0.16.2
[0.16.1]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/abdul-hamid-achik/mcphub/releases/tag/v0.1.0
