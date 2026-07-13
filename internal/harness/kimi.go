package harness

import "fmt"

// kimiAdapter handles Kimi Code CLI's ~/.kimi/config.toml, whose MCP servers
// live under "[mcp_servers.*]" tables. Kimi uses type "local"|"remote",
// flattens command+args into a "command" array, and names the env map
// "environment" — the same entry shape as opencode/kilo but in TOML.
//
// CAVEAT (like Codex): TOML is round-tripped through a generic map, so comments
// and key ordering in the file are not preserved. A timestamped .bak is always
// written before mutating, and `sync` defaults to dry-run, so this is safe.
type kimiAdapter struct{}

func (kimiAdapter) Kind() string { return "kimi" }

func kimiEntryFrom(s MCPServer) map[string]any {
	var e map[string]any
	if s.isRemote() {
		e = map[string]any{"type": "remote", "url": s.URL}
	} else {
		e = map[string]any{"type": "local"}
		if s.Command != "" {
			cmd := append([]string{s.Command}, s.Args...)
			e["command"] = toAnySlice(cmd)
		}
	}
	// environment applies to both shapes: the parser reads it back for every
	// entry, so omitting it on remotes would drop env and churn every sync.
	if len(s.Env) > 0 {
		envMap := map[string]any{}
		for k, v := range s.Env {
			envMap[k] = v
		}
		e["environment"] = envMap
	}
	return e
}

func (kimiAdapter) List(path string) ([]MCPServer, error) {
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	return sortedServers(kimiServers(root)), nil
}

// kimiServers parses the [mcp_servers.*] tables into name→server entries.
func kimiServers(root map[string]any) map[string]MCPServer {
	out := map[string]MCPServer{}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range servers {
		tbl, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		m := MCPServer{Name: name}
		if t, _ := tbl["type"].(string); t == "remote" || tbl["url"] != nil {
			if u, _ := tbl["url"].(string); u != "" {
				m.URL = u
			}
		} else if cmd, ok := tbl["command"].([]any); ok {
			if len(cmd) > 0 {
				m.Command, _ = cmd[0].(string)
				m.Args = toStringSlice(cmd[1:])
			}
		}
		if env, ok := tbl["environment"].(map[string]any); ok {
			m.Env = toStringMap(env)
		}
		out[name] = m
	}
	return out
}

func (kimiAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "kimi", Path: path}
	root, err := readTOML(path)
	if err != nil {
		return plan, err
	}
	desired = stripTransport(desired)
	existing := kimiServers(root)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	changed := changedSet(plan)
	// Overlay only the changed entries' managed keys, preserving any extra keys
	// (e.g. per-server tools/approval settings) the user added — same pattern as
	// codex, matching the package doc-comment's overlay promise.
	managedKeys := []string{"type", "command", "url", "environment"}
	for _, d := range desired {
		if !changed[d.Name] {
			continue
		}
		cur, _ := servers[d.Name].(map[string]any)
		if cur == nil {
			cur = map[string]any{}
		}
		for _, k := range managedKeys {
			delete(cur, k)
		}
		for k, v := range kimiEntryFrom(d) {
			cur[k] = v
		}
		servers[d.Name] = cur
	}
	desiredNames := names(desired)
	for _, name := range owned {
		if _, present := existing[name]; present && !contains(desiredNames, name) {
			delete(servers, name)
		}
	}
	root["mcp_servers"] = servers

	bak, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = bak
	if err := writeTOML(path, root); err != nil {
		return plan, fmt.Errorf("write %s: %w", path, err)
	}
	plan.Applied = true
	return plan, nil
}
