---
layout: home

hero:
  name: mcphub
  text: One hub for all your MCP servers.
  tagline: Define your MCP servers once, run a single gateway that proxies them all, and sync the right config into every agent. MCP Docker Kit, without Docker.
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/abdul-hamid-achik/mcphub

features:
  - title: Gateway & aggregation
    details: '`mcphub mcp serve` connects to every enabled downstream server as a client, aggregates their tools under namespaced `server__tool` names, and re-exposes them on ONE stdio connection. Your agent talks to one server instead of a dozen.'
  - title: Sync to every agent
    details: '`mcphub sync` writes the right MCP config into Claude Code, opencode, and Codex for you. Non-destructive merge, a timestamped .bak before every change, dry-run by default. Stop hand-editing each harness.'
  - title: Token savings
    details: 'In gateway mode each agent loads ONE server instead of every server''s full tool list. Fewer tools in context means fewer tokens spent before you''ve typed a word.'
  - title: Local intelligence
    details: 'Every proxied tool call is recorded in a local SQLite database. `mcphub stats` shows calls, errors, latency, and estimated token cost per server and tool — so you can see which servers earn their context budget.'
  - title: Studio TUI
    details: 'A bubbletea TUI to browse and toggle servers on and off, and watch usage stats live — without touching YAML or any agent config.'
  - title: YAML-first
    details: 'One `mcphub.yaml` is the single source of truth for which servers exist, how they group, and which agent harnesses mcphub keeps in sync. Edit it directly or from Studio.'
---
