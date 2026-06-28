package harness

import (
	"encoding/json"
	"fmt"
)

// claudeAdapter handles Claude Code's ~/.claude.json, whose MCP servers live
// under the top-level "mcpServers" object. Only that object is touched; every
// other key (projects, history, ui state, ...) is preserved verbatim.
type claudeAdapter struct{}

func (claudeAdapter) Kind() string { return "claude" }

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

func (claudeAdapter) List(path string) ([]MCPServer, error) {
	top, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing, err := claudeServers(top, path)
	if err != nil {
		return nil, err
	}
	return sortedServers(existing), nil
}

// claudeServers parses the mcpServers object into name→server entries.
func claudeServers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
	out := map[string]MCPServer{}
	raw, ok := top["mcpServers"]
	if !ok || len(raw) == 0 {
		return out, nil
	}
	servers := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("parse mcpServers in %s: %w", path, err)
	}
	for name, r := range servers {
		var e claudeEntry
		if json.Unmarshal(r, &e) == nil {
			out[name] = MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL, Transport: remoteTransport(e.URL, e.Type)}
		}
	}
	return out, nil
}

func (claudeAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "claude", Path: path}
	top, err := readJSONObject(path)
	if err != nil {
		return plan, err
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcpServers"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return plan, fmt.Errorf("parse mcpServers in %s: %w", path, err)
		}
	}
	existing, err := claudeServers(top, path)
	if err != nil {
		return plan, err
	}
	// claude writes a remote's transport into `type`, defaulting "" to "http";
	// mirror that so an unchanged remote isn't re-reported every sync.
	desired = defaultHTTPTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	mergeJSONServers(servers, existing, desired, owned, plan,
		[]string{"type", "command", "args", "env", "url"},
		func(d MCPServer) any { return claudeEntryFrom(d) })
	bak, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = bak
	top["mcpServers"] = mustIndentJSON(servers)
	if err := writeJSONObject(path, top); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}
