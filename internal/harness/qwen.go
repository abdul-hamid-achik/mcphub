package harness

import "encoding/json"

// qwenAdapter handles Qwen Code's ~/.qwen/settings.json, whose MCP servers
// live under the top-level "mcpServers" object. Qwen distinguishes transport
// by field name rather than a type tag: stdio uses command+args, HTTP uses
// "httpUrl", and SSE uses "url". Extra keys (headers, timeout, trust) are
// preserved as unmodeled.
var qwenAdapter = jsonAdapter{
	kind:        "qwen",
	key:         "mcpServers",
	managedKeys: []string{"command", "args", "env", "url", "httpUrl"},
	transport:   transportDefaultHTTP,
	entryFrom:   func(s MCPServer) any { return qwenEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e qwenEntry
		if json.Unmarshal(raw, &e) == nil {
			if e.HTTPUrl != "" {
				return MCPServer{Name: name, URL: e.HTTPUrl, Transport: "http"}, true
			}
			if e.URL != "" {
				return MCPServer{Name: name, URL: e.URL, Transport: "sse"}, true
			}
			return MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env}, true
		}
		return MCPServer{}, false
	},
}

type qwenEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	HTTPUrl string            `json:"httpUrl,omitempty"` // HTTP transport
	URL     string            `json:"url,omitempty"`     // SSE transport
}

func qwenEntryFrom(s MCPServer) qwenEntry {
	if s.isRemote() {
		if s.Transport == "sse" {
			return qwenEntry{URL: s.URL}
		}
		return qwenEntry{HTTPUrl: s.URL} // http (and "" defaults to http)
	}
	return qwenEntry{Command: s.Command, Args: s.Args, Env: s.Env}
}
