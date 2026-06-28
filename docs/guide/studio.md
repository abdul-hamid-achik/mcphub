# Studio (TUI)

Studio is mcphub's interactive terminal UI. Use it to browse your downstream
servers, toggle them on and off, and inspect local usage intelligence — without
hand-editing YAML or any agent's config file.

```sh
mcphub studio    # alias: tui
```

It is built on bubbletea and lipgloss and runs in the alternate screen, so it
takes over the terminal and restores it cleanly on exit.

## Layout

A header line shows the server/enabled/expose/agent counts, then three tabs:

- **Servers** — every server in `mcphub.yaml`, each showing an on/off mark, its
  name, kind, and description. The cursor (`▶`) marks the current row.
- **Agents** — each harness mcphub syncs into, with its type, mode, how many
  servers mcphub currently manages there, and its config path.
- **Stats** — the same local intelligence `mcphub stats` reports: total calls,
  estimated tokens, and errors, followed by spring-animated per-server bars.

Press `s` from any tab to open the **sync panel**: a dry-run preview of the
changes mcphub would make to every agent, which you can then apply in place.

## Keys

| Key                         | Action                                  |
| --------------------------- | --------------------------------------- |
| `↑` / `k`                   | move the cursor up                      |
| `↓` / `j`                   | move the cursor down                    |
| `space` / `enter`           | toggle the selected server on/off       |
| `s`                         | open the sync panel (preview → `a` apply) |
| `x`                         | toggle exposure (`all` ↔ `lazy`)        |
| `tab` / `→` / `l`           | next tab                                |
| `shift+tab` / `←` / `h`     | previous tab                            |
| `1` / `2` / `3`             | jump to Servers / Agents / Stats        |
| `r`                         | reload the config and stats from disk   |
| `q` / `ctrl+c`              | quit                                    |

## Toggling persists immediately

When you toggle a server with `space`, Studio writes the change to
`mcphub.yaml` right away — the same effect as `mcphub enable` / `mcphub disable`.
The status line confirms it and reminds you that enabling or disabling a server
only changes the config:

```
• vecgrep enabled — run `mcphub sync` to apply to agents
```

Toggling does **not** touch your agents. As with the CLI, you push the result to
your harnesses with [`mcphub sync`](/guide/sync) once you're happy with the set.

## Reloading

Press `r` to re-read `mcphub.yaml` and refresh the stats. This is handy if you
edited the config in your editor in another pane, or if an agent has made tool
calls through the gateway since you opened Studio and you want to see the
updated numbers.

## Relationship to the rest of mcphub

Studio is a view onto the exact same `mcphub.yaml` and intelligence store that
the CLI uses. Anything you do in Studio you can do from the command line and vice
versa:

- toggling a server ≡ [`mcphub enable` / `mcphub disable`](/reference/cli#enable-disable)
- the Stats tab ≡ [`mcphub stats`](/guide/intelligence)
- applying changes to agents ≡ [`mcphub sync`](/guide/sync)

## Next

- [Sync to your agents](/guide/sync) — push your toggles to every harness.
- [Intelligence](/guide/intelligence) — what the Stats tab is showing you.
