package mcp

import (
	"context"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Public names for the eight gateway meta-tools. Hosts that prefix the
// configured server name (Grok, local-agent) therefore expose
// `mcphub__list_servers` instead of `mcphub__mcphub_list_servers`.
const (
	toolListServers  = "list_servers"
	toolSearchTools  = "search_tools"
	toolDescribeTool = "describe_tool"
	toolResolveTool  = "resolve_tool"
	toolCallTool     = "call_tool"
	toolGetResult    = "get_result"
	toolPollResult   = "poll_result"
	toolStats        = "stats"
)

type managementTool struct {
	Public      string
	Title       string
	Description string
}

var managementTools = []managementTool{
	{
		Public:      toolListServers,
		Title:       "List servers",
		Description: "List configured downstream servers with enabled/connected state and tool counts.",
	},
	{
		Public:      toolSearchTools,
		Title:       "Search tools",
		Description: "Search and rank hidden downstream tools from natural-language intent. Matches tool metadata plus server descriptions, tags, and use_when routing hints; returns `server__tool` names for call_tool.",
	},
	{
		Public:      toolDescribeTool,
		Title:       "Describe tool",
		Description: "Return a downstream tool's description and full JSON input schema, so you can construct a valid call_tool request.",
	},
	{
		Public:      toolResolveTool,
		Title:       "Resolve tool",
		Description: "Contextual capability router. Call proactively when a task starts or changes phase: describe the current goal/activity in natural language and receive the best hidden tool, why it matched, required fields, an argument template, and ranked alternatives.",
	},
	{
		Public:      toolCallTool,
		Title:       "Call tool",
		Description: "Invoke a downstream tool by name. Oversized results return a lossless retrieval receipt for get_result; small results pass through unchanged. Accepts {server, tool, arguments} (tool may be the combined `server__tool` form). This is how you call tools in lazy mode. For long-running tools (repository indexing, large scans, batch jobs) that could exceed your client's tool-call timeout, pass detach: true — the call keeps running in the background and you get a callId immediately; collect the outcome with poll_result. An optional timeout_ms bounds the call (clamped by the gateway's call_timeout config).",
	},
	{
		Public:      toolGetResult,
		Title:       "Get result",
		Description: "Retrieve a bounded base64 page of a complete result previously stored by mcphub. Start with cursor 0 and continue with nextCursor until done is true.",
	},
	{
		Public:      toolPollResult,
		Title:       "Poll result",
		Description: "Check on a detached (detach: true) call_tool invocation by callId. Returns {status: pending} while the downstream call is still running (poll again after a delay), {status: failed} with the error if it failed, or — once complete — the tool's result itself, exactly as a synchronous call would have returned it (an oversized result appears as a stored-result receipt for get_result). Completed results are retained for 24 hours; detached calls do not survive a gateway restart, in which case the callId reports status: unknown.",
	},
	{
		Public:      toolStats,
		Title:       "Stats",
		Description: "Return local usage intelligence: total calls, error count, estimated token cost, and per-server breakdown recorded by the gateway.",
	},
}

func init() {
	if len(managementTools) != managementToolCount {
		panic("managementTools length does not match managementToolCount")
	}
}

var managementByPublic = func() map[string]managementTool {
	out := make(map[string]managementTool, len(managementTools))
	for _, tool := range managementTools {
		out[tool.Public] = tool
	}
	return out
}()

// canonicalManagementName maps a tools/call name onto the advertised public
// form. Hosts and older agents still send the self-prefixed wire names
// (mcphub_list_servers) or a harness-namespaced form (mcphub__list_servers,
// mcphub__mcphub_list_servers); those resolve here so they never 404.
func canonicalManagementName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	seen := make(map[string]struct{}, 4)
	candidates := make([]string, 0, 4)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		candidates = append(candidates, s)
	}
	add(name)
	if rest, ok := strings.CutPrefix(name, "mcphub__"); ok {
		add(rest)
		if rest2, ok := strings.CutPrefix(rest, "mcphub_"); ok {
			add(rest2)
		}
	}
	if rest, ok := strings.CutPrefix(name, "mcphub_"); ok {
		add(rest)
	}
	for _, candidate := range candidates {
		if _, ok := managementByPublic[candidate]; ok {
			return candidate, true
		}
	}
	return name, false
}

func rewriteManagementToolName(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		if method == "tools/call" {
			if params, ok := req.GetParams().(*sdk.CallToolParamsRaw); ok && params != nil {
				if pub, ok := canonicalManagementName(params.Name); ok {
					params.Name = pub
				}
			}
		}
		return next(ctx, method, req)
	}
}

func lookupManagement(name string) (managementTool, bool) {
	tool, ok := managementByPublic[name]
	return tool, ok
}
