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
		{"hitspec", "hitspec_search_web", "search_web"},
		{"hitspec", "search_web", "search_web"},
		{"hitspec", "hitspec_", "hitspec_"}, // empty remainder keeps full name
		{"live", "echo", "echo"},
		{"live", "live_echo", "echo"},
		{"", "x", "x"},
		{"s", "", ""},
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

func TestNamespacedAliases(t *testing.T) {
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
