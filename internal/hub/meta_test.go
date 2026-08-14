package hub

import "testing"

func TestResourceURIRoundTrip(t *testing.T) {
	uri := ResourceURI("hitspec", "file:///tmp/a b.md")
	server, original, ok := ParseResourceURI(uri)
	if !ok || server != "hitspec" || original != "file:///tmp/a b.md" {
		t.Fatalf("ParseResourceURI(%q) = %q %q %v", uri, server, original, ok)
	}
	if PromptName("codemap", "review") != "codemap__review" {
		t.Fatal("PromptName")
	}
}
