package mcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTruncateResultSmallPass(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "small result"}},
	}
	out := truncateResult(res, 1024, "test__tool")
	tc, ok := out.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if tc.Text != "small result" {
		t.Errorf("expected unchanged text, got %q", tc.Text)
	}
}

func TestTruncateResultLargeCapped(t *testing.T) {
	big := strings.Repeat("x", 5000)
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: big}},
	}
	out := truncateResult(res, 100, "test__tool")
	tc, ok := out.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !strings.Contains(tc.Text, "result truncated") {
		t.Errorf("expected truncation notice, got %q (len %d)", tc.Text[:min(50, len(tc.Text))], len(tc.Text))
	}
	if len(tc.Text) > 100+200 { // budget + notice length (generous)
		t.Errorf("expected text under budget+notice, got %d bytes", len(tc.Text))
	}
}

func TestResponseBudgetBytes(t *testing.T) {
	// Tested in config package, but verify the default here.
	// The truncateResult function is the integration point.
	res := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("y", 100)}},
	}
	// With a 50-byte budget, 100 bytes should be truncated.
	out := truncateResult(res, 50, "test__tool")
	tc, _ := out.Content[0].(*mcp.TextContent)
	if !strings.Contains(tc.Text, "truncated") {
		t.Errorf("expected truncation at 50 bytes for 100-byte input, got len %d", len(tc.Text))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
