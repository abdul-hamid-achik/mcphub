---
title: Bounded, lossless results
description: How mcphub spools oversized MCP tool results to SQLite for 24 hours and returns a compact recovery receipt instead of truncating the response.
---

# Bounded, lossless results

Some downstream tools return a lot: a full file tree, a large search result set,
a log dump. Fed straight through, one such call can blow past what you want in
an agent's context — or past the transport's own limits. The usual fix is
**truncation**, and truncation is a trap: the agent loses exactly the bytes it
might have needed, with no way to get them back.

mcphub does something different. Oversized results are never cut — they're
**spooled** to the local SQLite store in full, and the agent gets back a small
receipt it can use to page through the exact bytes on demand. Nothing is lost;
the agent just doesn't have to hold it all in context at once.

::: tip Why this beats truncation
Truncation is a one-way decision made *before* the agent knows whether the cut
part matters. Spooling defers that decision to the agent: small results pass
through unchanged, and an oversized result is fully recoverable — page it in
only if you actually need the rest.
:::

## How it works

Every successful call the gateway proxies goes through one finalize path in
`internal/hub`. It checks the complete serialized result against the
configured `response_budget`:

- **Under budget** — the result passes through unchanged. This is the common
  case; most tool calls never touch the spool at all.
- **Over budget** — mcphub writes the full serialized result to SQLite under
  an opaque `callId` and returns a compact **recovery receipt** instead of the
  result itself.

Spooling fails open: if the write to SQLite ever fails, the gateway logs a
warning and returns the complete result anyway. The one thing that never
happens is losing bytes.

The receipt is small by construction — it's built to fit inside the same
`response_budget` that triggered it — and looks like this:

```json
{
  "status": "stored",
  "callId": "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70",
  "server": "vecgrep",
  "tool": "search",
  "namespaced": "vecgrep__search",
  "originalBytes": 184320,
  "budgetBytes": 32768,
  "preview": "{\"matches\":[{\"file\":\"internal/hub/hub.go\"…",
  "nextAction": "Call mcphub_get_result with this callId and cursor 0, then continue with each nextCursor until done is true."
}
```

`preview` is a best-effort convenience — a leading slice of the result's text
content, included only if it still fits inside the budget alongside the rest
of the receipt. If the receipt's fixed fields alone would exceed the budget,
mcphub drops `server`/`tool`/`namespaced`/`nextAction` and keeps only what
retrieval actually requires: `callId`.

## Recovering the full result

Call [`mcphub_get_result`](/reference/meta-tools#mcphub-get-result) with the
`callId` and a byte `cursor` (start at `0`):

```json
{ "callId": "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70", "cursor": 0 }
```

Each call returns one bounded, base64-encoded page:

```json
{
  "status": "ok",
  "callId": "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70",
  "mediaType": "application/json",
  "data": "eyJtYXRjaGVzIjpb...",
  "cursor": 0,
  "nextCursor": 8192,
  "done": false,
  "totalBytes": 184320
}
```

Keep calling with `nextCursor` as the next `cursor` until `done` is `true`.
The page size adapts to your `response_budget` (capped at 8KB) so a page
always fits comfortably inside it, accounting for base64 expansion and the
surrounding envelope. Paging reads a byte range directly out of SQLite — the
gateway never re-materializes the whole payload in memory to serve a page.

If the `callId` is unknown or has expired, you get back a `status:
"unavailable"` response rather than an error, so the agent can decide what to
do next instead of a call just failing:

```json
{
  "status": "unavailable",
  "reason": "The callId is unknown or its stored result has expired.",
  "callId": "3f9a1c2e7b804d5e9f1a2b3c4d5e6f70"
}
```

A `cursor` past the end of the stored result comes back as `status:
"cursor_out_of_range"` instead of an error, too.

::: warning 24-hour retention
Spooled results live for a fixed **24 hours**, then they're pruned. There's no
config knob to extend it — retrieve what you need within the window, or
re-run the call.
:::

## Scope checks apply to retrieval too

`mcphub_get_result` isn't a bare key-value lookup — it re-checks the stored
result's `server`/`tool` against the calling agent's scope (the same
`servers`/`tools` routing rules from `mcphub.yaml`) before returning any bytes.
An agent scoped away from a server can't recover that server's spooled result
by guessing or reusing its `callId`, even though `callId` itself is an opaque
128-bit value with nothing to guess. See [per-agent routing](/guide/routing)
for how scoping is configured.

## Opting out

Two `mcphub.yaml` settings turn bounded-result spooling off entirely:

```yaml
response_budget: "0"  # unlimited — never spool, however large the result
verbatim: true        # pass every result through untouched, ignoring the budget
```

Both are top-level config keys (not per-server). With either set, the gateway
returns the complete result straight through, exactly as the downstream tool
produced it — useful when you trust your context budget more than you trust
truncation, or when a specific workflow needs the raw payload every time.

## Configuring the budget

```yaml
response_budget: 32KB   # default; accepts sizes like "32KB", "1MB", or "0" for unlimited
```

- Default is **32KB**. Below that, results pass through unchanged; above it,
  they're spooled.
- The smallest budget you can set (other than `0`) is **512 bytes** — the
  minimum size a retrieval receipt needs to fit.
- `response_budget` also caps how large a `mcphub_get_result` page can be, so
  the same knob bounds both directions.

## Summary

| Situation | What happens |
| --- | --- |
| Result ≤ `response_budget` | Passed through unchanged, as-is. |
| Result > `response_budget` | Spooled to SQLite; a receipt with `callId` is returned instead. |
| `verbatim: true` | Every result passes through, regardless of size. |
| `response_budget: "0"` | Unlimited — spooling is disabled entirely. |
| Recovering a stored result | Page it with `mcphub_get_result` (`callId` + `cursor`) until `done: true`. |
| Stored result age > 24h | `mcphub_get_result` returns `status: "unavailable"`. |
| Agent out of scope for the stored server/tool | `mcphub_get_result` errors instead of returning bytes. |

Every proxied call is also recorded to the same local store regardless of
whether it was spooled — see [Intelligence](/guide/intelligence) for what
`mcphub stats` does with that data.
