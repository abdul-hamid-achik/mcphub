package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRecordAndAggregate(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.RecordCall(ctx, CallRecord{Server: "s1", Tool: "t1", Namespaced: "s1__t1", Duration: 10 * time.Millisecond, ArgsBytes: 40, ResultBytes: 80}))
	must(st.RecordCall(ctx, CallRecord{Server: "s1", Tool: "t1", Namespaced: "s1__t1", Duration: 30 * time.Millisecond, Err: errors.New("boom"), ArgsBytes: 4, ResultBytes: 4}))
	must(st.RecordCall(ctx, CallRecord{Server: "s2", Tool: "t9", Namespaced: "s2__t9", Duration: 20 * time.Millisecond, ArgsBytes: 0, ResultBytes: 0}))

	tot, err := st.Totals(ctx)
	must(err)
	if tot.Calls != 3 {
		t.Errorf("calls = %d, want 3", tot.Calls)
	}
	if tot.Errors != 1 {
		t.Errorf("errors = %d, want 1", tot.Errors)
	}
	// est tokens: (120)/4 + (8)/4 + 0 = 30 + 2 = 32
	if tot.EstTokens != 32 {
		t.Errorf("est_tokens = %d, want 32", tot.EstTokens)
	}

	servers, err := st.ServerStats(ctx)
	must(err)
	if len(servers) != 2 {
		t.Fatalf("server stats = %d rows, want 2", len(servers))
	}
	// sorted by calls desc → s1 (2) first
	if servers[0].Server != "s1" || servers[0].Calls != 2 || servers[0].Errors != 1 {
		t.Errorf("top server = %+v", servers[0])
	}

	tools, err := st.ToolStats(ctx)
	must(err)
	if len(tools) != 2 {
		t.Errorf("tool stats = %d rows, want 2", len(tools))
	}
	// ToolStats ranks the hottest tool first (ORDER BY calls DESC).
	if tools[0].Tool != "t1" || tools[0].Calls != 2 {
		t.Errorf("top tool = %+v, want t1 with 2 calls", tools[0])
	}

	// RecentCalls is newest-first (id DESC) and honors the limit. The three
	// inserts above are s1/t1, s1/t1, s2/t9 -> ids 1,2,3.
	recent, err := st.RecentCalls(ctx, 2)
	must(err)
	if len(recent) != 2 {
		t.Fatalf("recent(2) = %d rows, want 2", len(recent))
	}
	if recent[0].Tool != "t9" || recent[1].Tool != "t1" {
		t.Errorf("recent order = [%s, %s], want [t9, t1]", recent[0].Tool, recent[1].Tool)
	}
	if recent[0].ID <= recent[1].ID {
		t.Errorf("recent must be id-descending: %d then %d", recent[0].ID, recent[1].ID)
	}
	all, err := st.RecentCalls(ctx, 100)
	must(err)
	if len(all) != 3 {
		t.Fatalf("recent(100) = %d rows, want 3", len(all))
	}
}

func TestEstTokens(t *testing.T) {
	if got := estTokens(40, 80); got != 30 {
		t.Errorf("estTokens(40,80) = %d, want 30", got)
	}
	if got := estTokens(0, 0); got != 0 {
		t.Errorf("estTokens(0,0) = %d, want 0", got)
	}
}

func TestCutoff(t *testing.T) {
	if cutoff(0) != allTimeCutoff {
		t.Errorf("cutoff(0) = %q, want all-time cutoff", cutoff(0))
	}
	// a window cutoff must be a real RFC3339 time well after the all-time floor
	if c := cutoff(time.Hour); c <= allTimeCutoff {
		t.Errorf("cutoff(1h) = %q should sort after the all-time floor", c)
	}
}

func TestWindowedStats(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if err := st.RecordCall(ctx, CallRecord{Server: "s", Tool: "t", Namespaced: "s__t", ArgsBytes: 4, ResultBytes: 4}); err != nil {
		t.Fatal(err)
	}
	// a generous window includes the just-recorded call...
	if tot, err := st.TotalsSince(ctx, time.Hour); err != nil || tot.Calls != 1 {
		t.Errorf("TotalsSince(1h) = %d calls (err %v), want 1", tot.Calls, err)
	}
	// ...a 1ns window excludes it (it was recorded just before now).
	if tot, err := st.TotalsSince(ctx, time.Nanosecond); err != nil || tot.Calls != 0 {
		t.Errorf("TotalsSince(1ns) = %d calls (err %v), want 0", tot.Calls, err)
	}
	// per-server windowed query runs and filters too
	if ss, err := st.ServerStatsSince(ctx, time.Hour); err != nil || len(ss) != 1 {
		t.Errorf("ServerStatsSince(1h) = %d rows (err %v), want 1", len(ss), err)
	}
	if ss, err := st.ServerStatsSince(ctx, time.Nanosecond); err != nil || len(ss) != 0 {
		t.Errorf("ServerStatsSince(1ns) = %d rows (err %v), want 0", len(ss), err)
	}
}

func TestManagedAndSyncLog(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	if err := st.SetManaged(ctx, "claude", []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ManagedFor(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("managed = %v, want 3", got)
	}

	// SetManaged replaces the full set, not appends.
	if err := st.SetManaged(ctx, "claude", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.ManagedFor(ctx, "claude")
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("managed after reset = %v, want [a]", got)
	}

	// A different agent is independent.
	if got, _ := st.ManagedFor(ctx, "opencode"); len(got) != 0 {
		t.Errorf("opencode managed = %v, want empty", got)
	}

	if err := st.LogSync(ctx, "claude", "gateway", []string{"mcphub"}, false); err != nil {
		t.Fatal(err)
	}
}
