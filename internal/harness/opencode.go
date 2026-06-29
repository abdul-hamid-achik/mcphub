package harness

import "encoding/json"

// opencodeAdapter handles opencode.json, whose MCP servers live under the
// top-level "mcp" object. opencode flattens command+args into a single
// "command" array and carries an "enabled" flag.
var opencodeAdapter = jsonAdapter{
	kind:        "opencode",
	key:         "mcp",
	managedKeys: []string{"type", "command", "enabled", "url", "environment"},
	transport:   transportStrip,
	entryFrom:   func(s MCPServer) any { return opencodeEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e opencodeEntry
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

type opencodeEntry struct {
	Type        string            `json:"type"` // "local" | "remote"
	Command     []string          `json:"command,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	URL         string            `json:"url,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

func opencodeEntryFrom(s MCPServer) opencodeEntry {
	if s.isRemote() {
		return opencodeEntry{Type: "remote", URL: s.URL, Enabled: boolPtr(true), Environment: s.Env}
	}
	cmd := append([]string{s.Command}, s.Args...)
	return opencodeEntry{Type: "local", Command: cmd, Enabled: boolPtr(true), Environment: s.Env}
}
