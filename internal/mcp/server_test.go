package mcp

import "testing"

func TestSplitNamespaced(t *testing.T) {
	cases := []struct {
		name                 string
		inServer, inTool     string
		wantServer, wantTool string
	}{
		{"combined", "", "srv__tool", "srv", "tool"},
		{"explicit unchanged", "srv", "tool", "srv", "tool"},
		{"combined first split only", "", "srv__a__b", "srv", "a__b"},
		{"explicit not resplit", "srv", "a__b", "srv", "a__b"},
		{"no separator", "", "noseparator", "", "noseparator"},
		// agent echoes the namespaced form into tool while also setting server.
		{"redundant prefix stripped", "codemap", "codemap__codemap_find", "codemap", "codemap_find"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotS, gotT := splitNamespaced(c.inServer, c.inTool)
			if gotS != c.wantServer || gotT != c.wantTool {
				t.Fatalf("splitNamespaced(%q,%q) = (%q,%q), want (%q,%q)",
					c.inServer, c.inTool, gotS, gotT, c.wantServer, c.wantTool)
			}
		})
	}
}
