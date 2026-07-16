---
layout: home
title: mcphub — one capability router for every MCP server
titleTemplate: false
description: Define your MCP servers once. mcphub resolves the right capability at runtime — including unpinned tools — proxies it through one gateway, and syncs 12 agent harnesses.

hero:
  name: mcphub
  text: Every MCP server. One hub. The right tool on demand.
  tagline: Connect each harness once. In lazy mode, agents can match the current task against your live catalog — including unpinned tools — then call the right capability through one gateway while mcphub keeps 12 agent configs in sync.
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: What is mcphub?
      link: /guide/concepts
    - theme: alt
      text: GitHub
      link: https://github.com/abdul-hamid-achik/mcphub

features:
  - icon: "01"
    title: Resolve capabilities by intent
    details: Add natural-language <code>use_when</code> hints once. Search and resolve index them with live tool metadata, so an agent can find the right server even when it is not pinned.
    link: /guide/lazy-mode
    linkText: Follow the discovery loop
  - icon: "02"
    title: One gateway, every tool
    details: Agents connect to a single stdio server. mcphub proxies every downstream server behind it and namespaces tools as <code>server__tool</code> — names never collide.
    link: /guide/concepts
    linkText: How the gateway works
  - icon: "03"
    title: Sync 12 harnesses
    details: <code>mcphub sync</code> writes the right config into Claude Code, Codex, opencode, Copilot CLI and eight more. Dry-run by default, timestamped .bak, hand-added servers survive.
    link: /guide/sync
    linkText: How sync stays safe
  - icon: "04"
    title: Token savings by design
    details: Gateway mode loads one server instead of a dozen tool lists. <code>expose&#58; lazy</code> goes further — eight meta-tools advertised, everything else discovered on demand.
    link: /guide/lazy-mode
    linkText: Lazy mode & pinning
  - icon: "05"
    title: Local intelligence
    details: Every proxied call lands in a local SQLite ledger. <code>mcphub stats</code> shows calls, errors, latency and estimated token cost per server and per tool.
    link: /guide/intelligence
    linkText: Read your stats
  - icon: "06"
    title: Lossless big results
    details: Oversized responses are spooled locally for 24 hours and replaced with a compact receipt — agents page back the exact bytes with <code>mcphub_get_result</code>. Nothing truncated.
    link: /guide/results
    linkText: Bounded results
---
