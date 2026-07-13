---
layout: home
title: mcphub — one hub for all your MCP servers
titleTemplate: false
description: Define your MCP servers once, run a single gateway that proxies them all, and sync the config into 12 agent harnesses. MCP Docker Kit, without Docker.

hero:
  name: mcphub
  text: Every MCP server. One hub. Every agent in sync.
  tagline: Define your servers once in mcphub.yaml. mcphub fronts them all on a single stdio connection, syncs 12 agent harnesses for you, and shows which tools actually earn their context budget.
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
  - icon: 🔌
    title: One gateway, every tool
    details: Agents connect to a single stdio server. mcphub proxies every downstream server behind it and exposes their tools as <code>server__tool</code> — names never collide.
    link: /guide/concepts
    linkText: How the gateway works
  - icon: 🔄
    title: Sync 12 harnesses
    details: <code>mcphub sync</code> writes the right config into Claude Code, Codex, opencode, Copilot CLI and eight more. Dry-run by default, timestamped .bak, hand-added servers survive.
    link: /guide/sync
    linkText: How sync stays safe
  - icon: 🪙
    title: Token savings by design
    details: Gateway mode loads one server instead of a dozen tool lists. <code>expose&#58; lazy</code> goes further — seven meta-tools advertised, everything else discovered on demand.
    link: /guide/lazy-mode
    linkText: Lazy mode & pinning
  - icon: 📊
    title: Local intelligence
    details: Every proxied call lands in a local SQLite ledger. <code>mcphub stats</code> shows calls, errors, latency and estimated token cost per server and per tool.
    link: /guide/intelligence
    linkText: Read your stats
  - icon: 📦
    title: Lossless big results
    details: Oversized responses are spooled locally for 24 hours and replaced with a compact receipt — agents page back the exact bytes with <code>mcphub_get_result</code>. Nothing truncated.
    link: /guide/results
    linkText: Bounded results
  - icon: 🔐
    title: Secrets never touch the config
    details: <code>vault&#58;</code> injects a TinyVault project's secrets at spawn, with allowlists for least privilege. Remote servers take <code>tvault&#58;//</code> refs in headers.
    link: /guide/secrets
    linkText: Secrets via tvault
---
