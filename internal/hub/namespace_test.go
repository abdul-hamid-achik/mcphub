package hub

import (
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPublicToolNameStripsSelfPrefix(t *testing.T) {
	cases := []struct {
		server, tool, want string
	}{
		// Underscore separator
		{"hitspec", "hitspec_search_web", "search_web"},
		{"hitspec", "search_web", "search_web"},
		{"hitspec", "hitspec_", "hitspec_"}, // empty remainder keeps full name
		{"live", "echo", "echo"},
		{"live", "live_echo", "echo"},
		// Hyphen separator
		{"bob", "bob-context", "context"},
		{"minerva", "minerva-search", "search"},
		{"foo", "foo-bar-baz", "bar-baz"},
		// Mixed - tool has both but server prefix comes first
		{"test", "test_foo-bar", "foo-bar"},
		{"test", "test-foo_bar", "foo_bar"},
		// No prefix
		{"", "x", "x"},
		{"s", "", ""},
		// No match
		{"server", "other_tool", "other_tool"},
	}
	for _, tc := range cases {
		if got := PublicToolName(tc.server, tc.tool); got != tc.want {
			t.Errorf("PublicToolName(%q, %q) = %q, want %q", tc.server, tc.tool, got, tc.want)
		}
	}
}

func TestPlanPublicNamesCollisionSafe(t *testing.T) {
	tools := []*mcp.Tool{
		{Name: "echo"},
		{Name: "live_echo"},
		{Name: "live_other"},
	}
	plan := PlanPublicNames("live", tools)
	want := map[string]string{
		"echo":       "echo",      // exact wins
		"live_echo":  "live_echo", // strip would collide with echo
		"live_other": "other",     // free to strip
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %#v, want %#v", plan, want)
	}
	if got := PublicNamespacedFor("live", "live_other", plan); got != "live__other" {
		t.Errorf("public name = %q", got)
	}
	if got := PublicNamespacedFor("live", "live_echo", plan); got != "live__live_echo" {
		t.Errorf("collision public name = %q", got)
	}
}

func TestPlanPublicNamesStripsSelfPrefix(t *testing.T) {
	tools := []*mcp.Tool{
		{Name: "hitspec_fetch"},
		{Name: "hitspec_search_web"},
	}
	plan := PlanPublicNames("hitspec", tools)
	if plan["hitspec_fetch"] != "fetch" || plan["hitspec_search_web"] != "search_web" {
		t.Fatalf("plan = %#v", plan)
	}
	if got := PublicNamespacedFor("hitspec", "hitspec_fetch", plan); got != "hitspec__fetch" {
		t.Errorf("namespaced = %q", got)
	}
}

func TestPlanPublicNamesHyphenSeparator(t *testing.T) {
	tools := []*mcp.Tool{
		{Name: "bob-context"},
		{Name: "bob-plan"},
	}
	plan := PlanPublicNames("bob", tools)
	if plan["bob-context"] != "context" || plan["bob-plan"] != "plan" {
		t.Fatalf("plan = %#v", plan)
	}
	if got := PublicNamespacedFor("bob", "bob-context", plan); got != "bob__context" {
		t.Errorf("namespaced = %q", got)
	}
}

func TestPlanPublicNamesCollisionAcrossSeparators(t *testing.T) {
	// If a server has both "srv_tool" and "srv-tool", they would both strip to "tool".
	// The collision detection should keep one full name to prevent the clash.
	// Processing in sorted order: "test-foo" comes before "test_foo", so "test-foo"
	// gets to claim "foo" and "test_foo" keeps its full name.
	tools := []*mcp.Tool{
		{Name: "test_foo"},
		{Name: "test-foo"},
	}
	plan := PlanPublicNames("test", tools)
	// One should strip, one should keep full name
	if plan["test-foo"] != "foo" {
		t.Errorf("test-foo should strip to foo, got %q", plan["test-foo"])
	}
	if plan["test_foo"] != "test_foo" {
		t.Errorf("test_foo should keep full name due to collision, got %q", plan["test_foo"])
	}

	// Another collision case: bare tool name vs stripped name
	tools2 := []*mcp.Tool{
		{Name: "context"},
		{Name: "bob-context"}, // Would strip to "context" — collision
	}
	plan2 := PlanPublicNames("bob", tools2)
	if plan2["context"] != "context" {
		t.Errorf("bare tool should keep its name: %#v", plan2)
	}
	if plan2["bob-context"] != "bob-context" {
		t.Errorf("prefixed tool should NOT strip when it would collide: %#v", plan2)
	}
}

func TestNamespacedAliases(t *testing.T) {
	// Test underscore-separated tool name
	aliases := NamespacedAliases("hitspec", "hitspec_fetch")
	wantAny := map[string]bool{
		"hitspec__fetch":         true,
		"hitspec__hitspec_fetch": true,
	}
	for _, a := range aliases {
		if !wantAny[a] {
			t.Errorf("unexpected alias %q in %v", a, aliases)
		}
		delete(wantAny, a)
	}
	for missing := range wantAny {
		t.Errorf("missing alias %q in %v", missing, aliases)
	}

	bare := NamespacedAliases("hitspec", "fetch")
	foundLegacy := false
	for _, a := range bare {
		if a == "hitspec__hitspec_fetch" {
			foundLegacy = true
		}
	}
	if !foundLegacy {
		t.Errorf("bare fragment aliases should include legacy stutter, got %v", bare)
	}

	// Test hyphen-separated tool name
	hyphAliases := NamespacedAliases("bob", "bob-context")
	wantHyph := map[string]bool{
		"bob__context":     true,
		"bob__bob-context": true,
	}
	for _, a := range hyphAliases {
		if !wantHyph[a] {
			t.Errorf("unexpected alias %q in %v", a, hyphAliases)
		}
		delete(wantHyph, a)
	}
	for missing := range wantHyph {
		t.Errorf("missing alias %q in %v", missing, hyphAliases)
	}
}

func TestAdmitNamespacedAcceptsLegacyPin(t *testing.T) {
	plan := PlanPublicNames("hitspec", []*mcp.Tool{{Name: "hitspec_fetch"}})
	// Predicate only knows the old stutter form.
	pred := func(ns string) bool { return ns == "hitspec__hitspec_fetch" }
	public, ok := admitNamespaced(pred, "hitspec", "hitspec_fetch", plan)
	if !ok {
		t.Fatal("legacy pin form should admit the tool")
	}
	if public != "hitspec__fetch" {
		t.Fatalf("mounted public name = %q, want hitspec__fetch", public)
	}
}

func TestNamespacedToolStripsSelfPrefix(t *testing.T) {
	tool := &mcp.Tool{Name: "hitspec_fetch", Description: "fetch one URL"}
	plan := PlanPublicNames("hitspec", []*mcp.Tool{tool})
	got := namespacedTool("hitspec", tool, plan)
	if got.Name != "hitspec__fetch" {
		t.Fatalf("mounted name = %q, want hitspec__fetch", got.Name)
	}
	if got.Description != "[hitspec] fetch one URL" {
		t.Fatalf("description = %q", got.Description)
	}
}
