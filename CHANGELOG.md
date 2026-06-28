# Changelog

All notable changes to mcphub are documented here. The format is loosely based
on [Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/) once it tags releases.

## [Unreleased]

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

[Unreleased]: https://github.com/abdul-hamid-achik/mcphub/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/abdul-hamid-achik/mcphub/releases/tag/v0.1.0
