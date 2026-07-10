package hub

import (
	"testing"
)

func TestParseVaultRef(t *testing.T) {
	cases := []struct {
		ref          string
		project, key string
		ok           bool
	}{
		{"tvault://obsidian/authorization", "obsidian", "authorization", true},
		{"tvault://default/api_key", "default", "api_key", true},
		{"tvault://API_KEY", "", "API_KEY", true},
		{"tvault://current/secret", "", "secret", true},
		{"tvault://obsidian/bearer%20token", "obsidian", "bearer%20token", true},
		{"tvault://", "", "", false},
		{"tvault:///key", "", "key", true},            // empty project → active
		{"tvault://obsidian/", "obsidian", "", false}, // empty key
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			project, key, ok := parseVaultRef(c.ref)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok {
				if project != c.project {
					t.Errorf("project = %q, want %q", project, c.project)
				}
				if key != c.key {
					t.Errorf("key = %q, want %q", key, c.key)
				}
			}
		})
	}
}

func TestResolveVaultHeadersPassthrough(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer literal-token",
		"X-Custom":      "plain-value",
		"X-No-Tvault":   "tvault is not in this value",
	}
	out, err := resolveVaultHeaders(in)
	if err != nil {
		t.Fatalf("resolveVaultHeaders: %v", err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("header %q = %q, want %q (passthrough)", k, out[k], v)
		}
	}
}

func TestResolveVaultHeadersRejectsMalformed(t *testing.T) {
	cases := []string{
		"tvault://",
		"tvault://obsidian/",
	}
	for _, val := range cases {
		t.Run(val, func(t *testing.T) {
			_, err := resolveVaultHeaders(map[string]string{"Authorization": val})
			if err == nil {
				t.Fatalf("expected error for %q, got nil", val)
			}
		})
	}
}
