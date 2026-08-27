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
//
// Most adapters keep their entries directly under a single top-level key.
// When `parent` is set, the entries live one level deeper (key within the
// parent object — ZCode reads "mcp":"servers") and every sibling of the
// parent object is still preserved verbatim.
type jsonAdapter struct {
	kind        string
	key         string // top-level JSON key ("mcpServers" or "mcp")
	parent      string // optional ancestor object holding key ("" = flat)
	managedKeys []string
	transport   transportPolicy
	entryFrom   func(MCPServer) any // serialize desired → entry
	parseEntry  func(name string, raw json.RawMessage) (MCPServer, bool)
}

// entryPath describes where the server entries sit inside the file, for
// error messages: "mcpServers", or "mcp.servers" when parent is set.
func (a jsonAdapter) entryPath() string {
	if a.parent == "" {
		return a.key
	}
	return a.parent + "." + a.key
}

// holder returns the JSON object the server entries belong to: `top` itself
// for flat adapters, or the parsed `parent` object (created empty if absent)
// for nested ones. It never mutates its input.
func (a jsonAdapter) holder(top map[string]json.RawMessage, path string) (map[string]json.RawMessage, error) {
	if a.parent == "" {
		return top, nil
	}
	nested := map[string]json.RawMessage{}
	if raw, ok := top[a.parent]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, fmt.Errorf("parse %s in %s: %w", a.parent, path, err)
		}
	}
	return nested, nil
}

// readEntries parses name→raw-entry out of the holder object; an absent key
// yields an empty map.
func (a jsonAdapter) readEntries(holder map[string]json.RawMessage, path string) (map[string]json.RawMessage, error) {
	entries := map[string]json.RawMessage{}
	if raw, ok := holder[a.key]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("parse %s in %s: %w", a.entryPath(), path, err)
		}
	}
	return entries, nil
}

// storeEntries merges the updated entries back into `top`, replacing the old
// value of the adapter's key (writing through the parent object when nested).
func (a jsonAdapter) storeEntries(top map[string]json.RawMessage, holder map[string]json.RawMessage, entries map[string]json.RawMessage) {
	if a.parent != "" {
		holder[a.key] = mustIndentJSON(entries)
		top[a.parent] = mustIndentJSON(holder)
		return
	}
	top[a.key] = mustIndentJSON(entries)
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

// servers parses the adapter's entry object into name→server entries.
func (a jsonAdapter) servers(top map[string]json.RawMessage, path string) (map[string]MCPServer, error) {
	holder, err := a.holder(top, path)
	if err != nil {
		return nil, err
	}
	raw, err := a.readEntries(holder, path)
	if err != nil {
		return nil, err
	}
	out := map[string]MCPServer{}
	for name, r := range raw {
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

	holder, err := a.holder(top, path)
	if err != nil {
		return plan, err
	}
	servers, err := a.readEntries(holder, path)
	if err != nil {
		return plan, err
	}
	mergeJSONServers(servers, existing, desired, owned, plan, a.managedKeys, a.entryFrom)
	bak, err := backup(path)
	if err != nil {
		return plan, err
	}
	plan.Backup = bak
	a.storeEntries(top, holder, servers)
	if err := writeJSONObject(path, top); err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}
