package harness

import "encoding/json"

// claudeAdapter handles Claude Code's ~/.claude.json, whose MCP servers live
// under the top-level "mcpServers" object. Only that object is touched; every
// other key (projects, history, ui state, ...) is preserved verbatim.
var claudeAdapter = jsonAdapter{
	kind:        "claude",
	key:         "mcpServers",
	managedKeys: []string{"type", "command", "args", "env", "url"},
	transport:   transportDefaultHTTP,
	entryFrom:   func(s MCPServer) any { return claudeEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e claudeEntry
		if json.Unmarshal(raw, &e) == nil {
			return MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Transport: remoteTransport(e.URL, e.Type)}, true
		}
		return MCPServer{}, false
	},
}

type claudeEntry struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func claudeEntryFrom(s MCPServer) claudeEntry {
	if s.isRemote() {
		t := s.Transport
		if t == "" {
			t = "http"
		}
		return claudeEntry{Type: t, URL: s.URL}
	}
	return claudeEntry{Command: s.Command, Args: s.Args, Env: s.Env}
}
