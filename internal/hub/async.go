package hub

// async.go implements detached (long-running) downstream calls. A detached
// call runs h.Call in the background — decoupled from the requesting agent's
// context, so the client's own tool-call timeout can no longer kill it — and
// parks the finalized result in a bounded in-memory registry keyed by an
// opaque call ID. The agent collects it with mcphub_poll_result.
//
// The registry deliberately reuses the hub's single Call path, so a detached
// call gets exactly the same telemetry, reconnect handling, and bounded
// lossless result policy as a synchronous one: an oversized detached result
// is already persisted in the SQLite spool by finalizeCall, and what the
// registry holds is the compact receipt. The registry itself is in-memory
// only; a gateway restart forgets pending and completed detached calls, and
// polling an unknown ID yields a clear "unknown" status rather than a hang.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxDetachedPending bounds concurrently running detached calls so a
	// misbehaving agent cannot fork unbounded downstream work.
	maxDetachedPending = 8
	// maxDetachedCompleted bounds retained finished results. Each entry is at
	// most one response budget (oversized payloads live in the SQLite spool,
	// only their compact receipts are retained here).
	maxDetachedCompleted = 128
	// detachedResultTTL matches the store's 24-hour spool retention, so a
	// receipt collected at the TTL boundary still points at a live payload.
	detachedResultTTL = 24 * time.Hour
	// defaultDetachedTimeout is the fallback ceiling when the caller passes a
	// nonpositive timeout; normal callers pass the config-clamped value.
	defaultDetachedTimeout = 30 * time.Minute
)

// DetachedStatus is the lifecycle state of a detached call.
type DetachedStatus string

// Detached call lifecycle states.
const (
	DetachedPending DetachedStatus = "pending"
	DetachedDone    DetachedStatus = "done"
	DetachedFailed  DetachedStatus = "failed"
)

// DetachedCall is one background downstream call tracked by the hub. Poll
// returns copies, so fields are safe to read without the registry lock.
type DetachedCall struct {
	ID          string
	Server      string
	Tool        string
	Namespaced  string
	Status      DetachedStatus
	Result      *mcp.CallToolResult // finalized result, set when Status == DetachedDone
	Err         string              // failure detail, set when Status == DetachedFailed
	StartedAt   time.Time
	CompletedAt time.Time // zero while pending
}

// StartDetached validates the target like Call, then runs the downstream call
// in the background under its own timeout and returns an opaque call ID
// immediately. The background context is detached from ctx on purpose: the
// whole point is surviving the requesting client's own tool-call deadline.
func (h *Hub) StartDetached(ctx context.Context, server, tool string, args json.RawMessage, timeout time.Duration) (string, error) {
	d := h.downstream(server)
	if d == nil {
		return "", fmt.Errorf("unknown server %q", server)
	}
	if !d.Connected() {
		return "", fmt.Errorf("server %q is not connected", server)
	}
	if _, ok := h.FindTool(server, tool); !ok {
		return "", fmt.Errorf("tool %q not found on server %q", tool, server)
	}
	if timeout <= 0 {
		timeout = defaultDetachedTimeout
	}

	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", fmt.Errorf("generate detached call ID: %w", err)
	}
	id := hex.EncodeToString(idBytes[:])

	call := &DetachedCall{
		ID:         id,
		Server:     server,
		Tool:       tool,
		Namespaced: server + "__" + tool,
		Status:     DetachedPending,
		StartedAt:  h.now(),
	}
	h.detachedMu.Lock()
	h.pruneDetachedLocked(h.now())
	if pending := h.countDetachedPendingLocked(); pending >= maxDetachedPending {
		h.detachedMu.Unlock()
		return "", fmt.Errorf("too many detached calls in flight (%d of %d); poll existing callIds or wait for one to finish", pending, maxDetachedPending)
	}
	if h.detached == nil {
		h.detached = map[string]*DetachedCall{}
	}
	h.detached[id] = call
	h.detachedMu.Unlock()

	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	go func() {
		defer cancel()
		res, err := h.Call(dctx, server, tool, args)
		h.detachedMu.Lock()
		defer h.detachedMu.Unlock()
		call.CompletedAt = h.now()
		if err != nil {
			call.Status = DetachedFailed
			call.Err = err.Error()
			return
		}
		call.Status = DetachedDone
		call.Result = res
	}()
	h.log.Info("detached call started", "server", server, "tool", tool, "callId", id, "timeout", timeout)
	return id, nil
}

// PollDetached returns a snapshot of a detached call. The second result is
// false when the ID is unknown — never issued, expired, evicted, or from
// before a gateway restart. Polling a completed call is idempotent: the entry
// stays until its retention window lapses.
func (h *Hub) PollDetached(id string) (DetachedCall, bool) {
	h.detachedMu.Lock()
	defer h.detachedMu.Unlock()
	h.pruneDetachedLocked(h.now())
	call, ok := h.detached[id]
	if !ok {
		return DetachedCall{}, false
	}
	return *call, true
}

// countDetachedPendingLocked counts in-flight detached calls. Callers hold
// detachedMu.
func (h *Hub) countDetachedPendingLocked() int {
	n := 0
	for _, c := range h.detached {
		if c.Status == DetachedPending {
			n++
		}
	}
	return n
}

// pruneDetachedLocked drops completed entries past their retention window and,
// if the completed set still exceeds its cap, evicts the oldest-finished
// entries. Pending entries are never pruned (they are bounded by the in-flight
// cap). Callers hold detachedMu.
func (h *Hub) pruneDetachedLocked(now time.Time) {
	if len(h.detached) == 0 {
		return
	}
	completed := make([]*DetachedCall, 0, len(h.detached))
	for id, c := range h.detached {
		if c.Status == DetachedPending {
			continue
		}
		if now.Sub(c.CompletedAt) > detachedResultTTL {
			delete(h.detached, id)
			continue
		}
		completed = append(completed, c)
	}
	if len(completed) <= maxDetachedCompleted {
		return
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].CompletedAt.Before(completed[j].CompletedAt) })
	for _, c := range completed[:len(completed)-maxDetachedCompleted] {
		delete(h.detached, c.ID)
	}
}
