package logsink

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAppendsToTodaysFile(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "gateway-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("world\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("log content = %q", string(data))
	}
	if !strings.Contains(w.Path(), time.Now().Format(dayFormat)) {
		t.Fatalf("path %q should carry today's date", w.Path())
	}
}

// A second writer with the same identity must append, not truncate: two
// gateways sharing a name share the file.
func TestSecondWriterAppends(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir, "g")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Write([]byte("a\n"))
	_ = a.Close()
	b, err := New(dir, "g")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = b.Write([]byte("b\n"))
	_ = b.Close()
	data, _ := os.ReadFile(b.Path())
	if string(data) != "a\nb\n" {
		t.Fatalf("expected append across writers, got %q", string(data))
	}
}

func TestMaintainCompressesOldAndPrunesAncient(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	today := now.Format(dayFormat)
	yesterday := now.AddDate(0, 0, -1).Format(dayFormat)

	old := filepath.Join(dir, "g-"+yesterday+".log")
	if err := os.WriteFile(old, []byte("old line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "g-"+today+".log")
	if err := os.WriteFile(current, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ancient := filepath.Join(dir, "g-2020-01-01.log.gz")
	if err := os.WriteFile(ancient, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := now.AddDate(0, 0, -retentionDays-1)
	if err := os.Chtimes(ancient, past, past); err != nil {
		t.Fatal(err)
	}

	maintainDir(dir, today, now)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("yesterday's .log should be replaced by .gz")
	}
	gz, err := os.Open(old + ".gz")
	if err != nil {
		t.Fatalf("expected compressed file: %v", err)
	}
	defer gz.Close()
	r, err := gzip.NewReader(gz)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(r)
	if string(content) != "old line\n" {
		t.Fatalf("gz content = %q", string(content))
	}
	if _, err := os.Stat(current); err != nil {
		t.Error("today's file must be left alone")
	}
	if _, err := os.Stat(ancient); !os.IsNotExist(err) {
		t.Error("files beyond retention should be pruned")
	}
}

func TestDefaultDirHonorsOff(t *testing.T) {
	t.Setenv("MCPHUB_LOG_DIR", "off")
	if got := DefaultDir(); got != "" {
		t.Fatalf("MCPHUB_LOG_DIR=off should disable, got %q", got)
	}
	t.Setenv("MCPHUB_LOG_DIR", "/tmp/somewhere")
	if got := DefaultDir(); got != "/tmp/somewhere" {
		t.Fatalf("override ignored, got %q", got)
	}
}
