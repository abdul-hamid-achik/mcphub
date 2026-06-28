package harness

import (
	"encoding/json"
	"fmt"
)

// opencodeAdapter handles opencode.json, whose MCP servers live under the
// top-level "mcp" object. opencode flattens command+args into a single
// "command" array and carries an "enabled" flag.
type opencodeAdapter struct{}

func (opencodeAdapter) Kind() string { return "opencode" }

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

func (opencodeAdapter) List(path string) ([]MCPServer, error) {
	top, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing, err := opencodeServers(top, path)
	if err != nil {
		return nil, err
	}
	return sortedServers(existing), nil
}

// opencodeServers parses the mcp object into name→server entries.
func opencodeServers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
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
		var e opencodeEntry
		if json.Unmarshal(r, &e) == nil {
			m := MCPServer{Name: name, URL: e.URL, Env: e.Environment}
			if len(e.Command) > 0 {
				m.Command = e.Command[0]
				m.Args = e.Command[1:]
			}
			out[name] = m
		}
	}
	return out, nil
}

func (opencodeAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "opencode", Path: path}
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
	existing, err := opencodeServers(top, path)
	if err != nil {
		return plan, err
	}
	// opencode.json can't represent http-vs-sse, so don't let Transport drive
	// the diff (it always reads back as "") or remote servers churn every sync.
	desired = stripTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	mergeJSONServers(servers, existing, desired, owned, plan,
		[]string{"type", "command", "enabled", "url", "environment"},
		func(d MCPServer) any { return opencodeEntryFrom(d) })
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
