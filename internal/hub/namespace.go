package hub

import (
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PublicToolName returns the tool fragment used after `server__` in the
// gateway-facing name. Downstream servers commonly self-prefix tools
// (hitspec's search tool is hitspec_search_web); stripping that redundant
// prefix keeps the public form clean (hitspec__search_web) instead of
// stuttering (hitspec__hitspec_search_web).
//
// The strip applies to both underscore and hyphen separators: if the tool name
// is `{server}_{rest}` or `{server}-{rest}`, the public name becomes `{rest}`.
// The strip only applies when the remainder is non-empty. Collision handling
// for a full server catalog lives in PlanPublicNames — this helper is the
// pure string rule used when no catalog is available.
func PublicToolName(server, downstreamTool string) string {
	if server == "" || downstreamTool == "" {
		return downstreamTool
	}
	// Try underscore separator first
	prefix := server + "_"
	if strings.HasPrefix(downstreamTool, prefix) {
		if rest := downstreamTool[len(prefix):]; rest != "" {
			return rest
		}
	}
	// Try hyphen separator
	prefix = server + "-"
	if strings.HasPrefix(downstreamTool, prefix) {
		if rest := downstreamTool[len(prefix):]; rest != "" {
			return rest
		}
	}
	return downstreamTool
}

// Namespaced is the preferred public gateway form for a downstream tool name:
// server__publicFragment. Prefer Hub.PublicNamespaced when a live tool catalog
// is available so collision-safe plans are honored.
func Namespaced(server, downstreamTool string) string {
	return server + "__" + PublicToolName(server, downstreamTool)
}

// LegacyNamespaced is the pre-strip form (server__downstreamTool) kept so pins,
// scopes, and callers that still use the stuttered name keep working.
func LegacyNamespaced(server, downstreamTool string) string {
	return server + "__" + downstreamTool
}

// NamespacedAliases returns every gateway name that may refer to
// (server, tool), where tool may be either the downstream wire name or the
// public fragment. Exact order is public-first, then legacy, then the raw
// combination — callers typically take the first hit.
func NamespacedAliases(server, tool string) []string {
	if server == "" || tool == "" {
		return nil
	}
	seen := make(map[string]struct{}, 4)
	out := make([]string, 0, 4)
	add := func(ns string) {
		if ns == "" {
			return
		}
		if _, ok := seen[ns]; ok {
			return
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}
	add(Namespaced(server, tool))
	add(LegacyNamespaced(server, tool))
	// Bare public fragment + reconstructed self-prefix (legacy stutter).
	// Check both underscore and hyphen separators.
	if !strings.HasPrefix(tool, server+"_") && !strings.HasPrefix(tool, server+"-") {
		add(LegacyNamespaced(server, server+"_"+tool))
		add(LegacyNamespaced(server, server+"-"+tool))
	}
	// Already self-prefixed: also accept the stripped public form explicitly
	// (Namespaced already added it when strip applies).
	if pub := PublicToolName(server, tool); pub != tool {
		add(server + "__" + pub)
	}
	return out
}

// PlanPublicNames maps each downstream tool.Name to the public fragment used
// after server__ when mounting/searching. Self-prefixes are stripped when that
// does not collide with another tool on the same server; on collision the
// exact downstream name is kept so a real tool can never be shadowed.
//
// Example on server "live" with tools {echo, live_echo}:
//
//	echo      → echo       (live__echo)
//	live_echo → live_echo  (live__live_echo) — strip would collide with echo
//
// Example on server "hitspec" with tools {hitspec_fetch}:
//
//	hitspec_fetch → fetch  (hitspec__fetch)
func PlanPublicNames(server string, tools []*mcp.Tool) map[string]string {
	out := make(map[string]string, len(tools))
	if len(tools) == 0 {
		return out
	}

	// Stable order so collision resolution is deterministic across refreshes.
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil || t.Name == "" {
			continue
		}
		names = append(names, t.Name)
	}
	sort.Strings(names)

	// Reserve every exact downstream name first so a bare tool always wins
	// over a self-prefixed peer that would strip onto it.
	claimed := make(map[string]string, len(names)) // public fragment → owner
	for _, name := range names {
		out[name] = name
		claimed[name] = name
	}

	for _, name := range names {
		pub := PublicToolName(server, name)
		if pub == name {
			continue
		}
		if owner, taken := claimed[pub]; taken && owner != name {
			// Keep the full downstream name (already in out).
			continue
		}
		// Free the previous reservation of the full name when we move off it.
		if claimed[name] == name {
			delete(claimed, name)
		}
		out[name] = pub
		claimed[pub] = name
	}
	return out
}

// PublicNamespacedFor returns the collision-safe public gateway name for a
// downstream tool given an explicit plan (from PlanPublicNames). Falls back to
// the pure strip rule when the tool is missing from the plan.
func PublicNamespacedFor(server, downstreamTool string, plan map[string]string) string {
	if plan != nil {
		if frag, ok := plan[downstreamTool]; ok && frag != "" {
			return server + "__" + frag
		}
	}
	return Namespaced(server, downstreamTool)
}
