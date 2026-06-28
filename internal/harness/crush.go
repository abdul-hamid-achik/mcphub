package harness

import (
	"encoding/json"
	"fmt"
)

// crushAdapter handles Crush's ~/.config/crush/crush.json, whose MCP servers
// live under the top-level "mcp" object. Crush entries carry an explicit
// "type" ("stdio" | "http" | "sse") alongside command/args or url.
type crushAdapter struct{}

func (crushAdapter) Kind() string { return "crush" }

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

func (crushAdapter) List(path string) ([]MCPServer, error) {
	top, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing, err := crushServers(top, path)
	if err != nil {
		return nil, err
	}
	return sortedServers(existing), nil
}

// crushServers parses the mcp object into name→server entries.
func crushServers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
	out := map[string]MCPServer{}
	raw, ok := top["mcp"]
	if !ok || len(raw) == 0 {
		return out, nil
	}
	servers := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("parse mcp in %s: %w", path, err)
	}
	for name, r := range servers {
		var e crushEntry
		if json.Unmarshal(r, &e) == nil {
			out[name] = MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Transport: remoteTransport(e.URL, e.Type)}
		}
	}
	return out, nil
}

func (crushAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "crush", Path: path}
	top, err := readJSONObject(path)
	if err != nil {
		return plan, err
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcp"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return plan, fmt.Errorf("parse mcp in %s: %w", path, err)
		}
	}
	existing, err := crushServers(top, path)
	if err != nil {
		return plan, err
	}
	// crush writes a remote's transport into `type`, defaulting "" to "http";
	// mirror that so an unchanged remote isn't re-reported every sync.
	desired = defaultHTTPTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	mergeJSONServers(servers, existing, desired, owned, plan,
		[]string{"type", "command", "args", "env", "url"},
		func(d MCPServer) any { return crushEntryFrom(d) })
	bak, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = bak
	top["mcp"] = mustIndentJSON(servers)
	if err := writeJSONObject(path, top); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}
