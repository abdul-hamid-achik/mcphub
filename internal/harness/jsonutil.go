package harness

import (
	"encoding/json"
	"fmt"
	"os"
)

// readJSONObject reads a JSON object file into a map of raw values, preserving
// every untouched key verbatim. A missing file yields an empty object.
func readJSONObject(path string) (map[string]json.RawMessage, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(trimSpace(body)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	return top, nil
}

// writeJSONObject writes a JSON object as indented JSON with a trailing
// newline. Untouched RawMessage values keep their original formatting.
func writeJSONObject(path string, top map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func mustIndentJSON(v any) json.RawMessage {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// All inputs here are plain structs/maps that always marshal.
		panic(err)
	}
	return b
}

// mergeJSONServers applies the changed desired entries into the raw `servers`
// map and prunes removed ones, for the JSON adapters (claude/opencode/crush).
// For each entry the diff adds or updates, it overlays the adapter's modeled
// keys onto whatever is already there — clearing only this adapter's
// `managedKeys` first — so any extra keys the user added to a managed entry
// (custom headers, timeouts, an explicit enabled flag) survive. Entries the
// diff leaves Unchanged are not touched at all. Owned entries no longer desired
// are deleted only when they are actually present, matching diff()'s
// ActionRemove condition so dry-run and write agree.
func mergeJSONServers(servers map[string]json.RawMessage, existing map[string]MCPServer, desired []MCPServer, owned []string, plan Plan, managedKeys []string, entry func(MCPServer) any) {
	changed := changedSet(plan)
	for _, d := range desired {
		if !changed[d.Name] {
			continue
		}
		cur := map[string]json.RawMessage{}
		if raw, ok := servers[d.Name]; ok {
			_ = json.Unmarshal(raw, &cur)
		}
		for _, k := range managedKeys {
			delete(cur, k)
		}
		fresh := map[string]json.RawMessage{}
		if b, err := json.Marshal(entry(d)); err == nil {
			_ = json.Unmarshal(b, &fresh)
		}
		for k, v := range fresh {
			cur[k] = v
		}
		servers[d.Name] = mustIndentJSON(cur)
	}
	desiredNames := names(desired)
	for _, name := range owned {
		if _, present := existing[name]; present && !contains(desiredNames, name) {
			delete(servers, name)
		}
	}
}

func names(servers []MCPServer) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Name)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\n' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\n' || b[j-1] == '\t' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
