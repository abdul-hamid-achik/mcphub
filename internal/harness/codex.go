package harness

import (
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

// codexAdapter handles Codex's ~/.codex/config.toml, whose MCP servers live
// under the "[mcp_servers.*]" tables.
//
// CAVEAT: TOML is round-tripped through a generic map, so comments and key
// ordering in the file are not preserved. A timestamped .bak is always written
// before mutating, and `sync` defaults to dry-run, so this is safe but worth
// knowing. Only the mcp_servers subtree is logically changed.
type codexAdapter struct{}

func (codexAdapter) Kind() string { return "codex" }

func codexEntryFrom(s MCPServer) map[string]any {
	if s.isRemote() {
		return map[string]any{"url": s.URL}
	}
	e := map[string]any{"command": s.Command}
	if len(s.Args) > 0 {
		e["args"] = toAnySlice(s.Args)
	}
	if len(s.Env) > 0 {
		env := make(map[string]any, len(s.Env))
		for k, v := range s.Env {
			env[k] = v
		}
		e["env"] = env
	}
	return e
}

func (codexAdapter) List(path string) ([]MCPServer, error) {
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	return sortedServers(codexServers(root)), nil
}

// codexServers parses the [mcp_servers.*] tables into name→server entries.
func codexServers(root map[string]any) map[string]MCPServer {
	out := map[string]MCPServer{}
	servers, _ := root["mcp_servers"].(map[string]any)
	for name, raw := range servers {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := MCPServer{Name: name}
		if cmd, ok := m["command"].(string); ok {
			entry.Command = cmd
		}
		if url, ok := m["url"].(string); ok {
			entry.URL = url
		}
		if args, ok := m["args"].([]any); ok {
			entry.Args = toStringSlice(args)
		}
		if env, ok := m["env"].(map[string]any); ok {
			entry.Env = toStringMap(env)
		}
		out[name] = entry
	}
	return out
}

func (codexAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "codex", Path: path}
	root, err := readTOML(path)
	if err != nil {
		return plan, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	existing := codexServers(root)
	// codex's TOML can't represent http-vs-sse, so don't let Transport drive
	// the diff (it always reads back as "") or remote servers churn every sync.
	desired = stripTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	// Overlay only the changed entries' managed keys, preserving any extra keys
	// (e.g. per-server tools/approval settings codex supports) the user added.
	changed := changedSet(plan)
	for _, d := range desired {
		if !changed[d.Name] {
			continue
		}
		cur, _ := servers[d.Name].(map[string]any)
		if cur == nil {
			cur = map[string]any{}
		}
		for _, k := range []string{"command", "args", "env", "url"} {
			delete(cur, k)
		}
		for k, v := range codexEntryFrom(d) {
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
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

func readTOML(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := toml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

func writeTOML(path string, root map[string]any) error {
	out, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func toStringSlice(s []any) []string {
	out := make([]string, 0, len(s))
	for _, v := range s {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func toStringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if str, ok := v.(string); ok {
			out[k] = str
		}
	}
	return out
}
