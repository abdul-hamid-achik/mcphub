package hub

import "context"

type callViaKey struct{}

const (
	ViaMount    = "mount"
	ViaCallTool = "call_tool"
	ViaDetach   = "detach"
)

// ContextWithVia tags a Call so telemetry records how the agent invoked it.
func ContextWithVia(ctx context.Context, via string) context.Context {
	if via == "" {
		return ctx
	}
	return context.WithValue(ctx, callViaKey{}, via)
}

func viaFrom(ctx context.Context) string {
	if ctx == nil {
		return ViaMount
	}
	if v, ok := ctx.Value(callViaKey{}).(string); ok && v != "" {
		return v
	}
	return ViaMount
}
