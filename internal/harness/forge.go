package harness

import (
	"encoding/json"
	"fmt"
)

// forgeAdapter handles Forge (forgecode) — `.mcp.json`, JSON with a top-level
// "mcpServers" object (the same shape Claude uses), except each entry carries a
// `disable` boolean rather than a `type`. Like the other JSON adapters it
// preserves every other key, including any extra per-entry keys, byte-for-byte.
type forgeAdapter struct{}

func (forgeAdapter) Kind() string { return "forge" }

type forgeEntry struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Disable bool              `json:"disable"`
}

func forgeEntryFrom(s MCPServer) forgeEntry {
	if s.isRemote() {
		return forgeEntry{URL: s.URL, Disable: false}
	}
	return forgeEntry{Command: s.Command, Args: s.Args, Env: s.Env, Disable: false}
}

func (forgeAdapter) List(path string) ([]MCPServer, error) {
	top, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing, err := forgeServers(top, path)
	if err != nil {
		return nil, err
	}
	return sortedServers(existing), nil
}

func forgeServers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
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
		var e forgeEntry
		if json.Unmarshal(r, &e) == nil {
			out[name] = MCPServer{Name: name, Command: e.Command, Args: e.Args, Env: e.Env, URL: e.URL}
		}
	}
	return out, nil
}

func (forgeAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "forge", Path: path}
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
	existing, err := forgeServers(top, path)
	if err != nil {
		return plan, err
	}
	// Forge can't represent http-vs-sse, so don't let Transport drive the diff.
	desired = stripTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	mergeJSONServers(servers, existing, desired, owned, plan,
		[]string{"command", "args", "env", "url", "disable"},
		func(d MCPServer) any { return forgeEntryFrom(d) })
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
