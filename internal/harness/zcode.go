package harness

// zcodeAdapter handles ZCode's ~/.zcode/cli/config.json. Entries have the
// same shape as Claude's mcpServers values (command/args/env, or type+url with
// stdio/http inferred when type is omitted), but the servers live under the
// nested "mcp"."servers" object instead of a top-level key. Host-side extras
// mcphub does not model (cwd, enabled, timeoutMs, per-entry headers) are not
// managed keys, so they survive sync on already-managed entries.
var zcodeAdapter = func() jsonAdapter {
	a := claudeStyleAdapter("zcode")
	a.parent = "mcp"
	a.key = "servers"
	return a
}()
