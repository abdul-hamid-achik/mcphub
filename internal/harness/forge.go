package harness

import "encoding/json"

// forgeAdapter handles Forge (forgecode) — `.mcp.json`, JSON with a top-level
// "mcpServers" object (the same shape Claude uses), except each entry carries a
// `disable` boolean rather than a `type`. Like the other JSON adapters it
// preserves every other key, including any extra per-entry keys, byte-for-byte.
var forgeAdapter = jsonAdapter{
	kind:        "forge",
	key:         "mcpServers",
	managedKeys: []string{"command", "args", "env", "url"},
	transport:   transportStrip,
	entryFrom:   func(s MCPServer) any { return forgeEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e forgeEntry
		if json.Unmarshal(raw, &e) == nil {
			return MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL}, true
		}
		return MCPServer{}, false
	},
}

type forgeEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Disable bool              `json:"disable,omitempty"`
}

func forgeEntryFrom(s MCPServer) forgeEntry {
	if s.isRemote() {
		return forgeEntry{URL: s.URL, Disable: false}
	}
	return forgeEntry{Command: s.Command, Args: s.Args, Env: s.Env, Disable: false}
}
