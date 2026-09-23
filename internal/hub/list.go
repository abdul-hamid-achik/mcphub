package hub

// list.go pages through downstream list RPCs. The SDK applies a server-side
// page size (configurable via ServerOptions.PageSize on the remote end), so a
// single nil-params call silently truncates any catalog that spans more than
// one page. These helpers follow NextCursor to the end instead.

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxListPages bounds cursor-following per RPC: a downstream that keeps
// returning the same cursor (a server bug, or an adversarial one) must not loop
// the gateway forever. Even the smallest practical page size serves orders of
// magnitude more pages than any real catalog needs.
const maxListPages = 1000

// listAllTools returns the downstream's complete tool catalog, following
// pagination cursors.
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	var tools []*mcp.Tool
	var cursor string
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("tools/list exceeded %d pages (downstream cursor loop?)", maxListPages)
		}
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if list == nil {
			break
		}
		tools = append(tools, list.Tools...)
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	return tools, nil
}

// listAllResources returns the downstream's complete resource catalog, following
// pagination cursors. Downstreams without the resources capability return an
// error, which callers treat as an empty catalog.
func listAllResources(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Resource, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	var resources []*mcp.Resource
	var cursor string
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("resources/list exceeded %d pages (downstream cursor loop?)", maxListPages)
		}
		list, err := session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if list == nil {
			break
		}
		resources = append(resources, list.Resources...)
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	return resources, nil
}

// listAllPrompts returns the downstream's complete prompt catalog, following
// pagination cursors. Downstreams without the prompts capability return an
// error, which callers treat as an empty catalog.
func listAllPrompts(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Prompt, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	var prompts []*mcp.Prompt
	var cursor string
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("prompts/list exceeded %d pages (downstream cursor loop?)", maxListPages)
		}
		list, err := session.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if list == nil {
			break
		}
		prompts = append(prompts, list.Prompts...)
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	return prompts, nil
}
