package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestResultSpoolPagesAndBoundaries(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	payload := append([]byte(`{"content":[{"type":"text","text":"`), bytes.Repeat([]byte("é-data-"), 25)...)
	payload = append(payload, []byte(`"}],"structuredContent":{"ok":true}}`)...)

	callID, err := st.PutResult(ctx, "alpha", "large", payload)
	if err != nil {
		t.Fatal(err)
	}
	idBytes, err := hex.DecodeString(callID)
	if err != nil || len(idBytes) != 16 {
		t.Fatalf("call ID %q is not opaque 128-bit hex", callID)
	}

	var rebuilt []byte
	var cursor int64
	for {
		page, err := st.ReadResultPage(ctx, callID, cursor, 17)
		if err != nil {
			t.Fatal(err)
		}
		if page.Server != "alpha" || page.Tool != "large" {
			t.Fatalf("stored scope = %s__%s", page.Server, page.Tool)
		}
		if page.Cursor != cursor || page.TotalBytes != int64(len(payload)) {
			t.Fatalf("page offsets = %+v, payload bytes = %d", page, len(payload))
		}
		rebuilt = append(rebuilt, page.Data...)
		cursor = page.NextCursor
		if page.Done {
			break
		}
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatal("paged payload did not reconstruct byte-for-byte")
	}

	end, err := st.ReadResultPage(ctx, callID, int64(len(payload)), 17)
	if err != nil || !end.Done || len(end.Data) != 0 || end.NextCursor != int64(len(payload)) {
		t.Fatalf("cursor at end = %+v, err %v", end, err)
	}
	beyond, err := st.ReadResultPage(ctx, callID, int64(len(payload))+1, 17)
	if !errors.Is(err, ErrResultCursorOutOfRange) || beyond.Server != "alpha" || beyond.Tool != "large" {
		t.Fatalf("cursor beyond end page = %+v, error = %v", beyond, err)
	}
	if _, err := st.ReadResultPage(ctx, "unknown", 0, 17); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("unknown call ID error = %v", err)
	}
	if _, err := st.ReadResultPage(ctx, callID, -1, 17); !errors.Is(err, ErrResultCursorOutOfRange) {
		t.Fatalf("negative cursor error = %v", err)
	}
}

func TestResultSpoolExpiryAndOpportunisticPrune(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }

	expiredID, err := st.PutResult(ctx, "alpha", "old", []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(resultTTL)
	if _, err := st.ReadResultPage(ctx, expiredID, 0, 8); !errors.Is(err, ErrResultExpired) {
		t.Fatalf("expired result error = %v", err)
	}

	liveID, err := st.PutResult(ctx, "alpha", "new", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadResultPage(ctx, expiredID, 0, 8); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("insert should prune expired row, got %v", err)
	}
	if page, err := st.ReadResultPage(ctx, liveID, 0, 8); err != nil || string(page.Data) != "new" {
		t.Fatalf("live result = %+v, err %v", page, err)
	}
}

func TestResultSpoolConcurrentPutAndPage(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	const workers = 24
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := []byte(fmt.Sprintf(`{"worker":%d,"value":"%s"}`, i, strings.Repeat("x", 97+i)))
			callID, err := st.PutResult(ctx, "race", fmt.Sprintf("tool-%d", i), payload)
			if err != nil {
				errs <- err
				return
			}
			var rebuilt []byte
			var cursor int64
			for {
				page, err := st.ReadResultPage(ctx, callID, cursor, 11)
				if err != nil {
					errs <- err
					return
				}
				rebuilt = append(rebuilt, page.Data...)
				cursor = page.NextCursor
				if page.Done {
					break
				}
			}
			if !bytes.Equal(rebuilt, payload) {
				errs <- fmt.Errorf("worker %d reconstructed different bytes", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestResultSpoolMigrationIsIdempotentAndOpenPrunes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.sql.Exec(`
		INSERT INTO result_spool (call_id, server, tool, created_at, expires_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"expired-on-open", "s", "t", "1999-01-01T00:00:00Z", "2000-01-01T00:00:00Z", []byte("old"),
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reapplying idempotent migrations: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.ReadResultPage(context.Background(), "expired-on-open", 0, 8); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("open-time prune error = %v", err)
	}
}

func TestStoreFilePermissionsProtectSpooledResults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(dir, "results.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("store directory mode = %o, want 700", got)
	}
	dbInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := dbInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("store database mode = %o, want 600", got)
	}
}

func TestStoreLeavesExistingParentPermissionsUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "results.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("caller-owned directory mode = %o, want unchanged 755", got)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, statErr := os.Stat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", filepath.Base(candidate), got)
		}
	}
}

func TestResultSpoolPrunerUsesChronologicalExpiryWhileGatewayRuns(t *testing.T) {
	st := newStore(t)
	base := time.Date(2026, 7, 10, 12, 0, 0, 910_000_000, time.UTC)
	now := base
	st.now = func() time.Time { return now }
	callID, err := st.PutResult(context.Background(), "s", "t", []byte("still-private"))
	if err != nil {
		t.Fatal(err)
	}

	triggerPrune := func() {
		t.Helper()
		done := make(chan struct{})
		st.pruneNow <- done
		<-done
	}
	now = base.Add(resultTTL - 10*time.Millisecond)
	triggerPrune()
	if _, err := st.ReadResultPage(context.Background(), callID, 0, 8); err != nil {
		t.Fatalf("result pruned before its chronological expiry: %v", err)
	}

	now = base.Add(resultTTL + 10*time.Millisecond)
	triggerPrune()
	if _, err := st.ReadResultPage(context.Background(), callID, 0, 8); !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("live pruner left expired payload on disk: %v", err)
	}
}
