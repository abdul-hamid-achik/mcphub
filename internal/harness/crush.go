package harness

import "encoding/json"

// crushAdapter handles Crush's ~/.config/crush/crush.json, whose MCP servers
// live under the top-level "mcp" object. Crush entries carry an explicit
// "type" ("stdio" | "http" | "sse") alongside command/args or url.
var crushAdapter = jsonAdapter{
	kind:        "crush",
	key:         "mcp",
	managedKeys: []string{"type", "command", "args", "env", "url"},
	transport:   transportDefaultHTTP,
	entryFrom:   func(s MCPServer) any { return crushEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e crushEntry
		if json.Unmarshal(raw, &e) == nil {
			return MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Transport: remoteTransport(e.URL, e.Type)}, true
		}
		return MCPServer{}, false
	},
}

type crushEntry struct {
	Type    string            `json:"type,omitempty"` // stdio | http | sse
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func crushEntryFrom(s MCPServer) crushEntry {
	if s.isRemote() {
		t := s.Transport
		if t == "" {
			t = "http"
		}
		return crushEntry{Type: t, URL: s.URL}
	}
	return crushEntry{Type: "stdio", Command: s.Command, Args: s.Args, Env: s.Env}
}
