package harness

import (
	"sort"
	"strings"
)

// localAgentAdapter handles local-agent's YAML config, whose MCP servers are
// stored as a top-level sequence under `servers`.
type localAgentAdapter struct{}

func (localAgentAdapter) Kind() string { return "local-agent" }

func localAgentEntryFrom(server MCPServer) map[string]any {
	entry := map[string]any{"name": server.Name}
	if server.isRemote() {
		entry["url"] = server.URL
		if server.Transport == "sse" {
			entry["transport"] = "sse"
		} else {
			entry["transport"] = "streamable-http"
		}
		return entry
	}

	entry["command"] = server.Command
	if len(server.Args) > 0 {
		entry["args"] = toAnySlice(server.Args)
	}
	if len(server.Env) > 0 {
		keys := make([]string, 0, len(server.Env))
		for key := range server.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		env := make([]any, 0, len(keys))
		for _, key := range keys {
			env = append(env, key+"="+server.Env[key])
		}
		entry["env"] = env
	}
	return entry
}

func localAgentServers(root map[string]any) map[string]MCPServer {
	result := map[string]MCPServer{}
	entries, _ := root["servers"].([]any)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		server := MCPServer{Name: name}
		server.Command, _ = entry["command"].(string)
		server.URL, _ = entry["url"].(string)
		if transport, _ := entry["transport"].(string); server.URL != "" {
			if transport == "sse" {
				server.Transport = "sse"
			} else {
				server.Transport = "http"
			}
		}
		if args, ok := entry["args"].([]any); ok {
			server.Args = toStringSlice(args)
		}
		if env, ok := entry["env"].([]any); ok {
			server.Env = map[string]string{}
			for _, rawValue := range env {
				value, ok := rawValue.(string)
				if !ok {
					continue
				}
				key, val, found := strings.Cut(value, "=")
				if found && key != "" {
					server.Env[key] = val
				}
			}
		}
		result[name] = server
	}
	return result
}

func (localAgentAdapter) List(path string) ([]MCPServer, error) {
	root, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	return sortedServers(localAgentServers(root)), nil
}

func (localAgentAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: "local-agent", Path: path}
	root, err := readYAML(path)
	if err != nil {
		return plan, err
	}
	existing := localAgentServers(root)
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	desiredByName := make(map[string]MCPServer, len(desired))
	for _, server := range desired {
		desiredByName[server.Name] = server
	}
	desiredNames := names(desired)
	ownedSet := make(map[string]struct{}, len(owned))
	for _, name := range owned {
		ownedSet[name] = struct{}{}
	}
	changed := changedSet(plan)
	seen := map[string]struct{}{}
	entries, _ := root["servers"].([]any)
	updated := make([]any, 0, len(entries)+len(desired))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			updated = append(updated, raw)
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			updated = append(updated, raw)
			continue
		}
		if _, managed := ownedSet[name]; managed && !contains(desiredNames, name) {
			continue
		}
		server, wanted := desiredByName[name]
		if wanted {
			seen[name] = struct{}{}
			if changed[name] {
				for _, key := range []string{"command", "args", "env", "url", "transport"} {
					delete(entry, key)
				}
				for key, value := range localAgentEntryFrom(server) {
					entry[key] = value
				}
			}
		}
		updated = append(updated, entry)
	}
	for _, server := range desired {
		if _, present := seen[server.Name]; !present {
			updated = append(updated, localAgentEntryFrom(server))
		}
	}
	root["servers"] = updated

	backupPath, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = backupPath
	if err := writeYAML(path, root); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

var _ Adapter = localAgentAdapter{}
