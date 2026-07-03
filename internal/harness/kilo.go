package harness

import "encoding/json"

// kiloAdapter handles Kilo Code's ~/.config/kilo/kilo.jsonc, whose MCP servers
// live under the top-level "mcp" object. Kilo uses type "local"|"remote",
// flattens command+args into a single "command" array, and names the env map
// "environment" — the same shape as opencode. The file is JSONC (JSON with
// comments); readJSONObject strips comments before parsing so .jsonc works
// the same as .json. Comments are not preserved on write (a .bak is taken
// first, matching the codex/hermes caveat).
var kiloAdapter = jsonAdapter{
	kind:        "kilo",
	key:         "mcp",
	managedKeys: []string{"type", "command", "url", "environment"},
	transport:   transportStrip,
	entryFrom:   func(s MCPServer) any { return kiloEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e kiloEntry
		if json.Unmarshal(raw, &e) == nil {
			m := MCPServer{Name: name, URL: e.URL, Env: e.Environment}
			if len(e.Command) > 0 {
				m.Command = e.Command[0]
				m.Args = e.Command[1:]
			}
			return m, true
		}
		return MCPServer{}, false
	},
}

type kiloEntry struct {
	Type        string            `json:"type"` // "local" | "remote"
	Command     []string          `json:"command,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	URL         string            `json:"url,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

func kiloEntryFrom(s MCPServer) kiloEntry {
	if s.isRemote() {
		return kiloEntry{Type: "remote", URL: s.URL, Environment: s.Env}
	}
	cmd := append([]string{s.Command}, s.Args...)
	return kiloEntry{Type: "local", Command: cmd, Environment: s.Env}
}
