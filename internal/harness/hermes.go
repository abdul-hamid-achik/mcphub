package harness

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// hermesAdapter handles Hermes — `~/.hermes/config.yaml`, a large YAML config
// whose MCP servers live under a top-level "mcp_servers" map (entries carry an
// `enabled` flag). Only the mcp_servers subtree is logically changed and a
// timestamped .bak is always written first.
//
// CAVEAT (like Codex): the YAML is round-tripped through a generic map, so on a
// write the whole file is reserialized — every key's VALUE is preserved, but
// comments and key ordering elsewhere are not.
type hermesAdapter struct{}

func (hermesAdapter) Kind() string { return "hermes" }

func hermesEntryFrom(s MCPServer) map[string]any {
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

func (hermesAdapter) List(path string) ([]MCPServer, error) {
	root, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	return sortedServers(hermesServers(root)), nil
}

func hermesServers(root map[string]any) map[string]MCPServer {
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

func (hermesAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "hermes", Path: path}
	root, err := readYAML(path)
	if err != nil {
		return plan, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	existing := hermesServers(root)
	// Hermes can't represent http-vs-sse, so don't let Transport drive the diff.
	desired = stripTransport(desired)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

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
		for k, v := range hermesEntryFrom(d) {
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
	if err := writeYAML(path, root); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

func readYAML(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if len(body) == 0 {
		return root, nil
	}
	if err := yaml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeYAML(path string, root map[string]any) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
