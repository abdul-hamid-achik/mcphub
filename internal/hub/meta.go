package hub

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func listResources(ctx context.Context, session *mcp.ClientSession) []*mcp.Resource {
	if session == nil {
		return nil
	}
	list, err := session.ListResources(ctx, nil)
	if err != nil || list == nil {
		return nil
	}
	return list.Resources
}

func listPrompts(ctx context.Context, session *mcp.ClientSession) []*mcp.Prompt {
	if session == nil {
		return nil
	}
	list, err := session.ListPrompts(ctx, nil)
	if err != nil || list == nil {
		return nil
	}
	return list.Prompts
}

// ResourceURI is the gateway-facing URI for a downstream resource.
func ResourceURI(server, original string) string {
	return "mcphub://" + url.PathEscape(server) + "/" + url.PathEscape(original)
}

// ParseResourceURI splits a gateway resource URI into server + original URI.
func ParseResourceURI(uri string) (server, original string, ok bool) {
	if !strings.HasPrefix(uri, "mcphub://") {
		return "", "", false
	}
	rest := strings.TrimPrefix(uri, "mcphub://")
	serverEnc, origEnc, found := strings.Cut(rest, "/")
	if !found || serverEnc == "" {
		return "", "", false
	}
	server, err1 := url.PathUnescape(serverEnc)
	original, err2 := url.PathUnescape(origEnc)
	if err1 != nil || err2 != nil || server == "" || original == "" {
		return "", "", false
	}
	return server, original, true
}

// PromptName is the namespaced prompt advertised by the gateway.
func PromptName(server, prompt string) string {
	return server + "__" + prompt
}

// MountResourcesAndPrompts registers downstream resources and prompts onto srv.
func (h *Hub) MountResourcesAndPrompts(srv *mcp.Server, allowServer func(string) bool) {
	for _, d := range h.Downstreams() {
		if !d.Connected() || (allowServer != nil && !allowServer(d.Name)) {
			continue
		}
		name := d.Name
		for _, res := range d.ResourcesSnapshot() {
			if res == nil || res.URI == "" {
				continue
			}
			mounted := *res
			orig := res.URI
			mounted.URI = ResourceURI(name, orig)
			if mounted.Description != "" {
				mounted.Description = "[" + name + "] " + mounted.Description
			} else {
				mounted.Description = "[" + name + "]"
			}
			srv.AddResource(&mounted, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				session, err := h.sessionFor(name)
				if err != nil {
					return nil, err
				}
				return session.ReadResource(ctx, &mcp.ReadResourceParams{URI: orig})
			})
		}
		for _, p := range d.PromptsSnapshot() {
			if p == nil || p.Name == "" {
				continue
			}
			mounted := *p
			orig := p.Name
			mounted.Name = PromptName(name, orig)
			if mounted.Description != "" {
				mounted.Description = "[" + name + "] " + mounted.Description
			}
			srv.AddPrompt(&mounted, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				session, err := h.sessionFor(name)
				if err != nil {
					return nil, err
				}
				params := &mcp.GetPromptParams{Name: orig}
				if req != nil && req.Params != nil {
					params.Arguments = req.Params.Arguments
				}
				return session.GetPrompt(ctx, params)
			})
		}
	}
}

func (h *Hub) sessionFor(server string) (*mcp.ClientSession, error) {
	d, err := h.awaitDownstream(context.Background(), server)
	if err != nil {
		return nil, err
	}
	session, connectionErr := d.connectionSnapshot()
	if session == nil || connectionErr != nil {
		return nil, notConnectedError(server, d)
	}
	return session, nil
}

// CatalogWhatIf compares serialized downstream tool-definition cost for expose
// all versus lazy+pins (management tools are not included).
type CatalogWhatIf struct {
	ConnectedServers int `json:"connected_servers"`
	AllTools         int `json:"all_tools"`
	AllBytes         int `json:"all_definition_bytes"`
	AllEstTokens     int `json:"all_est_tokens"`
	PinTools         int `json:"pin_tools"`
	PinBytes         int `json:"pin_definition_bytes"`
	PinEstTokens     int `json:"pin_est_tokens"`
	SavedBytes       int `json:"saved_definition_bytes"`
	SavedEstTokens   int `json:"saved_est_tokens"`
}

func (h *Hub) CatalogWhatIf() CatalogWhatIf {
	all := h.MatchingTools(func(string) bool { return true })
	pins := h.MatchingTools(func(ns string) bool {
		return h.cfg != nil && h.cfg.PinMatches(ns)
	})
	var allBytes, pinBytes int
	for _, m := range all {
		if m.Definition == nil {
			continue
		}
		b, err := json.Marshal(m.Definition)
		if err == nil {
			allBytes += len(b)
		}
	}
	for _, m := range pins {
		if m.Definition == nil {
			continue
		}
		b, err := json.Marshal(m.Definition)
		if err == nil {
			pinBytes += len(b)
		}
	}
	connected := 0
	for _, d := range h.Downstreams() {
		if d.Connected() {
			connected++
		}
	}
	out := CatalogWhatIf{
		ConnectedServers: connected,
		AllTools:         len(all),
		AllBytes:         allBytes,
		AllEstTokens:     estimatedDefinitionTokens(allBytes),
		PinTools:         len(pins),
		PinBytes:         pinBytes,
		PinEstTokens:     estimatedDefinitionTokens(pinBytes),
	}
	if allBytes > pinBytes {
		out.SavedBytes = allBytes - pinBytes
		out.SavedEstTokens = out.AllEstTokens - out.PinEstTokens
	}
	return out
}
