---
title: Studio (TUI)
description: mcphub studio is a bubbletea v2 terminal UI for toggling servers, previewing and applying a sync, flipping exposure, and watching live usage stats.
---

# Studio (TUI)

Studio is mcphub's interactive terminal UI — the same `mcphub.yaml` and
[intelligence store](/guide/intelligence) the CLI uses, browsable and toggleable
without hand-editing YAML or an agent's config file. It's built on
`charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`, with
`charmbracelet/harmonica` driving spring-animated stat bars on the Stats tab.

```sh
mcphub studio    # alias: tui
```

It runs in the alternate screen, so it takes over the terminal and restores it
cleanly on exit.

## Layout

A header line shows the server/enabled/expose/agent counts, then three tabs:

- **Servers** — every server in `mcphub.yaml`, each showing an on/off mark, a
  `[pin]` marker for [pinned](/guide/lazy-mode) servers, its name, kind
  (stdio or remote), discovery state, and description. The cursor (`▶`) marks
  the current row. The selected server expands its `use_when` routing hints and
  tags, so you can audit what an agent will match against without opening YAML.
- **Agents** — each harness mcphub syncs into, with its type, mode, how many
  servers mcphub currently manages there, and its config path. Agents with
  [per-agent routing](/guide/routing) show their `servers`/`tools` scope on a
  second line. Agents that have a config file on disk but aren't declared in
  `mcphub.yaml` show up as **available** — add them to `agents:` (or run
  `mcphub init --from-agents`) to start syncing them.
- **Stats** — the same local intelligence [`mcphub stats`](/guide/intelligence)
  reports: total calls, estimated tokens, and errors, followed by
  harmonica spring-animated per-server call bars that ease toward their target
  length whenever you switch onto the tab.

## Keys

| Key | Action |
| --- | --- |
| `↑` / `k`, `↓` / `j` | move the cursor |
| `space` / `enter` | toggle the selected server on/off (Servers tab) |
| `p` | pin/unpin the selected server (Servers tab) |
| `s` | open the sync panel — preview a dry run, then `a` to apply |
| `a` / `enter` | apply the previewed sync (inside the sync panel) |
| `esc` / `q` | close the sync panel without applying |
| `x` | flip exposure between `all` and `lazy` |
| `tab` / `→` / `l`, `shift+tab` / `←` / `h` | next / previous tab |
| `1` / `2` / `3` | jump to Servers / Agents / Stats |
| `r` | reload `mcphub.yaml` and the stats from disk |
| `q` / `ctrl+c` | quit |

## Toggling persists immediately

Press `space` on a server and Studio writes the change to `mcphub.yaml` right
away — the same effect as [`mcphub enable`](/reference/cli#enable) /
[`mcphub disable`](/reference/cli#disable).
The status line confirms it and reminds you that toggling only changes the
config:

```
• vecgrep enabled — press s to sync agents
```

Toggling does **not** touch your agents. As with the CLI, you push the result to
your harnesses with `s` in Studio, or [`mcphub sync`](/guide/sync) from the
shell, once you're happy with the set.

## Reading discovery state

The Servers tab shows the **global configured exposure policy**, not a live
connection check or one agent's route. That distinction matters in lazy mode:
removing a pin should save context without making an eligible capability look
lost. A tool is actually reachable only when its server is connected and the
selected agent's `servers` / `tools` scope allows it. Direct-mode agents bypass
gateway exposure and receive their enabled, in-scope servers directly.

| State | Meaning |
| --- | --- |
| `advertised` | Policy advertises every allowed tool. In lazy mode this means the whole server is pinned; with `expose: all`, every enabled server has this state. Connection and agent scope still apply. |
| `on-demand` | The server is enabled and unpinned. Once connected and in scope, its tools stay out of the up-front list but can be found through search/resolve. |
| `mixed` | One or more exact tools are pinned; other allowed tools are eligible on demand once connected. |
| `unavailable` | The server is disabled in the registry, so the gateway will not connect to it. |

Move the cursor onto a server to preview its natural-language `use_when` hints.
Studio keeps this to two terminal lines and shows `+N more` when needed; the
full list remains in `mcphub.yaml` and `mcphub_list_servers`. Search and resolve
index every hint alongside live tool metadata, so make them describe task
signals an agent would actually have, such as “capture a public webpage as
Markdown for research” or “inspect repository convergence before feature
work.”

Pressing `p` advertises every tool on the selected server. If the state was
`mixed`, this upgrades the exact pins to a whole-server pin; press `p` again to
remove all pins for that server. In lazy mode the status line confirms that an
unpinned server remains eligible for on-demand discovery when connected and
allowed by the agent's scope.

## Preview then apply: `s` → `a`

Press `s` from any tab to open the **sync panel**: a dry-run preview of the
exact changes mcphub would make to every agent, computed with the same
reconcile engine [`mcphub sync`](/guide/sync) uses. Nothing is written yet.
Press `a` (or `enter`) to apply it — this is the equivalent of running
`mcphub sync --write` from the shell, backup and all — or `esc` to back out.
The panel always covers every enabled agent; to sync just one, use
`mcphub sync <agent> --write` from the shell instead.

::: tip Preview costs nothing
Opening the sync panel never mutates anything — it's the same dry-run guarantee
as the CLI. You can open it just to see current drift, then back out without
applying.
:::

## Flipping exposure: `x`

`x` toggles the top-level `expose` field in `mcphub.yaml` between `all` (every
downstream tool mounted as `server__tool`) and `lazy` (only the eight gateway
meta-tools advertised, with the rest discovered on demand). See
[Exposure: all vs. lazy](/guide/concepts#exposure-all-vs-lazy) for what each
mode costs and buys you. As with a pin change, an exposure flip takes effect
the next time `mcphub mcp serve` starts — restart your agents to pick it up.

## Reloading

Press `r` to re-read `mcphub.yaml` and refresh the stats. Handy if you edited
the config in another pane, or an agent has made calls through the gateway
since you opened Studio and you want to see the updated numbers.

## Studio vs. the CLI

Studio and the CLI operate on the exact same files — nothing you do in one is
invisible to the other. Reach for whichever fits the moment:

- **Studio** for interactive sessions: skimming what's enabled across a dozen
  servers, toggling a handful on or off while eyeballing the effect, checking
  which agents have drifted, or watching which servers are actually earning
  calls before you decide what to disable.
- **The CLI** for anything scripted, repeatable, or automated: CI, a one-off
  `mcphub enable codemap && mcphub sync --write`, `--json`/`--markdown` output
  for tooling or notes, or driving mcphub from another agent.

Everything maps directly:

| Studio | CLI equivalent |
| --- | --- |
| `space` on a server | [`mcphub enable`](/reference/cli#enable) / [`mcphub disable`](/reference/cli#disable) |
| `p` on a server | [`mcphub pin`](/reference/cli#pin) / [`mcphub unpin`](/reference/cli#unpin) |
| `x` | editing `expose:` in `mcphub.yaml` |
| `s` → `a` | [`mcphub sync`](/guide/sync) → `mcphub sync --write` |
| Stats tab | [`mcphub stats`](/guide/intelligence) |
| Agents tab | [`mcphub agents`](/reference/cli#agents) / [`mcphub status`](/reference/cli#status) |

## Next

- [Sync to your agents](/guide/sync) — what the sync panel's preview and apply
  actually do, per harness.
- [Intelligence](/guide/intelligence) — what the Stats tab is showing you.
- [Concepts](/guide/concepts) — gateway vs. direct, exposure, and pins.
