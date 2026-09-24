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
// returning fresh cursors (a server bug, or an adversarial one) must not loop
// the gateway forever. Even the smallest practical page size serves orders of
// magnitude more pages than any real catalog needs.
const maxListPages = 1000

// maxListItems bounds how many entries one catalog may accumulate, so a
// downstream serving huge pages cannot make the gateway hold millions of
// definitions (with schemas) in memory before maxListPages trips.
const maxListItems = 20000

// listAll follows pagination cursors for one list RPC. It fails fast when a
// cursor does not advance, and bounds both pages and total items.
func listAll[T any](what string, fetch func(cursor string) (items []T, next string, err error)) ([]T, error) {
	var all []T
	var cursor string
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("%s exceeded %d pages (downstream cursor loop?)", what, maxListPages)
		}
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(all) > maxListItems {
			return nil, fmt.Errorf("%s exceeded %d entries", what, maxListItems)
		}
		if next == "" {
			return all, nil
		}
		if next == cursor {
			return nil, fmt.Errorf("%s returned the same cursor twice (downstream cursor loop)", what)
		}
		cursor = next
	}
}

// listAllTools returns the downstream's complete tool catalog, following
// pagination cursors.
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	return listAll("tools/list", func(cursor string) ([]*mcp.Tool, string, error) {
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil || list == nil {
			return nil, "", err
		}
		return list.Tools, list.NextCursor, nil
	})
}

// listAllResources returns the downstream's complete resource catalog, following
// pagination cursors. Downstreams without the resources capability return an
// error, which callers treat as an empty catalog.
func listAllResources(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Resource, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	return listAll("resources/list", func(cursor string) ([]*mcp.Resource, string, error) {
		list, err := session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil || list == nil {
			return nil, "", err
		}
		return list.Resources, list.NextCursor, nil
	})
}

// listAllPrompts returns the downstream's complete prompt catalog, following
// pagination cursors. Downstreams without the prompts capability return an
// error, which callers treat as an empty catalog.
func listAllPrompts(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Prompt, error) {
	if session == nil {
		return nil, fmt.Errorf("no session")
	}
	return listAll("prompts/list", func(cursor string) ([]*mcp.Prompt, string, error) {
		list, err := session.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
		if err != nil || list == nil {
			return nil, "", err
		}
		return list.Prompts, list.NextCursor, nil
	})
}
