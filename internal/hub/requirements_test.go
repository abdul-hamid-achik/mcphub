package hub

import "testing"

// TestRequirementsExamples verifies all the examples from the user requirements.
func TestRequirementsExamples(t *testing.T) {
	testCases := []struct {
		server   string
		toolName string
		want     string
	}{
		// Underscore-separated examples from requirements
		{"bob", "bob_context", "bob__context"},
		{"hitspec", "hitspec_crawl", "hitspec__crawl"},
		{"mcphub", "mcphub_tool_call", "mcphub__tool_call"},
		{"cortex", "cortex_open_task", "cortex__open_task"},
		{"codemap", "codemap_context", "codemap__context"},
		{"vecgrep", "vecgrep_search", "vecgrep__search"},
		// Hyphen-separated examples (inferred from requirements)
		{"bob", "bob-context", "bob__context"},
		{"fcheap", "fcheap-save", "fcheap__save"},
		// No prefix - should remain unchanged
		{"server", "foo", "server__foo"},
	}

	for _, tc := range testCases {
		got := Namespaced(tc.server, tc.toolName)
		if got != tc.want {
			t.Errorf("Namespaced(%q, %q) = %q, want %q", tc.server, tc.toolName, got, tc.want)
		}
	}
}

// TestRequirementsBackwardCompatibility verifies legacy stutter names still resolve.
func TestRequirementsBackwardCompatibility(t *testing.T) {
	legacyTests := []struct {
		server   string
		toolName string
		legacy   string
	}{
		{"bob", "bob_context", "bob__bob_context"},
		{"hitspec", "hitspec_crawl", "hitspec__hitspec_crawl"},
		{"mcphub", "mcphub_tool_call", "mcphub__mcphub_tool_call"},
		{"bob", "bob-context", "bob__bob-context"},
	}

	for _, tc := range legacyTests {
		aliases := NamespacedAliases(tc.server, tc.toolName)
		found := false
		for _, a := range aliases {
			if a == tc.legacy {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("legacy alias %q not found in NamespacedAliases(%q, %q); got %v",
				tc.legacy, tc.server, tc.toolName, aliases)
		}
	}
}
