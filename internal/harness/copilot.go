package harness

import "encoding/json"

// copilotAdapter handles GitHub Copilot CLI's ~/.copilot/mcp-config.json,
// whose MCP servers live under the top-level "mcpServers" object — the same
// shape Claude uses, except every entry carries an explicit "type"
// ("local"/"stdio" | "http" | "sse") and may include "tools", "headers", and
// "timeout" keys (left untouched as unmodeled). Like the other JSON adapters
// it preserves every other key byte-for-byte.
var copilotAdapter = jsonAdapter{
	kind:        "copilot",
	key:         "mcpServers",
	managedKeys: []string{"type", "command", "args", "env", "url"},
	transport:   transportDefaultHTTP,
	entryFrom:   func(s MCPServer) any { return copilotEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e copilotEntry
		if json.Unmarshal(raw, &e) == nil {
			return MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Transport: remoteTransport(e.URL, e.Type)}, true
		}
		return MCPServer{}, false
	},
}

type copilotEntry struct {
	Type    string            `json:"type,omitempty"` // local | stdio | http | sse
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func copilotEntryFrom(s MCPServer) copilotEntry {
	if s.isRemote() {
		t := s.Transport
		if t == "" {
			t = "http"
		}
		return copilotEntry{Type: t, URL: s.URL}
	}
	return copilotEntry{Type: "stdio", Command: s.Command, Args: s.Args, Env: s.Env}
}
