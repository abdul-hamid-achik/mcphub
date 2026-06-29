package harness

import (
	"encoding/json"
	"fmt"
)

// transportPolicy controls how Transport is adjusted before diffing so a remote
// server is not falsely reported as changed on every sync.
type transportPolicy int

const (
	// transportKeep leaves Transport as-is (the adapter's entries have no
	// transport/type field, so the parsed Transport is always "").
	transportKeep transportPolicy = iota
	// transportDefaultHTTP mirrors the adapter's write-time default of "" →
	// "http" on the desired side, so an unchanged remote doesn't churn.
	transportDefaultHTTP
	// transportStrip clears Transport on the desired side because the adapter's
	// format can't represent http-vs-sse (it always reads back as "").
	transportStrip
)

// jsonAdapter is the shared implementation for JSON-based harness adapters
// (claude, opencode, crush, forge). Each differs only in the top-level key,
// the entry shape, the managed keys, and how transport is compared — all
// captured here as fields so List/Apply are fully generic.
type jsonAdapter struct {
	kind        string
	key         string // top-level JSON key ("mcpServers" or "mcp")
	managedKeys []string
	transport   transportPolicy
	entryFrom   func(MCPServer) any // serialize desired → entry
	parseEntry  func(name string, raw json.RawMessage) (MCPServer, bool)
}

func (a jsonAdapter) Kind() string { return a.kind }

func (a jsonAdapter) List(path string) ([]MCPServer, error) {
	top, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	existing, err := a.servers(top, path)
	if err != nil {
		return nil, err
	}
	return sortedServers(existing), nil
}

// servers parses the adapter's top-level key into name→server entries.
func (a jsonAdapter) servers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
	out := map[string]MCPServer{}
	raw, ok := top[a.key]
	if !ok || len(raw) == 0 {
		return out, nil
	}
	entries := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse %s in %s: %w", a.key, path, err)
	}
	for name, r := range entries {
		if m, ok := a.parseEntry(name, r); ok {
			out[name] = m
		}
	}
	return out, nil
}

func (a jsonAdapter) Apply(path string, desired []MCPServer, owned []string, dryRun bool) (Plan, error) {
	plan := Plan{Kind: a.kind, Path: path}
	top, err := readJSONObject(path)
	if err != nil {
		return plan, err
	}
	existing, err := a.servers(top, path)
	if err != nil {
		return plan, err
	}
	switch a.transport {
	case transportDefaultHTTP:
		desired = defaultHTTPTransport(desired)
	case transportStrip:
		desired = stripTransport(desired)
	}
	plan.Changes = diff(existing, desired, owned)
	if dryRun || !plan.HasChanges() {
		return plan, nil
	}

	servers := map[string]json.RawMessage{}
	if raw, ok := top[a.key]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return plan, fmt.Errorf("parse %s in %s: %w", a.key, path, err)
		}
	}
	mergeJSONServers(servers, existing, desired, owned, plan, a.managedKeys, a.entryFrom)
	bak, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = bak
	top[a.key] = mustIndentJSON(servers)
	if err := writeJSONObject(path, top); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}
