package harness

import "encoding/json"

// geminiAdapter handles Gemini CLI's ~/.gemini/settings.json, whose MCP
// servers live under the top-level "mcpServers" object. Gemini uses the same
// field-name convention as Qwen: stdio → command+args, HTTP → "httpUrl",
// SSE → "url". Extra keys (headers, timeout, trust, includeTools) are
// preserved as unmodeled.
var geminiAdapter = jsonAdapter{
	kind:        "gemini",
	key:         "mcpServers",
	managedKeys: []string{"command", "args", "env", "url", "httpUrl"},
	transport:   transportDefaultHTTP,
	entryFrom:   func(s MCPServer) any { return geminiEntryFrom(s) },
	parseEntry: func(name string, raw json.RawMessage) (MCPServer, bool) {
		var e geminiEntry
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

type geminiEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	HTTPUrl string            `json:"httpUrl,omitempty"` // HTTP transport
	URL     string            `json:"url,omitempty"`     // SSE transport
}

func geminiEntryFrom(s MCPServer) geminiEntry {
	if s.isRemote() {
		if s.Transport == "sse" {
			return geminiEntry{URL: s.URL}
		}
		return geminiEntry{HTTPUrl: s.URL} // http (and "" defaults to http)
	}
	return geminiEntry{Command: s.Command, Args: s.Args, Env: s.Env}
}
