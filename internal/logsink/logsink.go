// Package logsink gives the gateway a durable log destination. In gateway
// mode stderr belongs to the parent agent and dies with its session — which
// is exactly when the logs are needed: the question is always "what did that
// gateway see" after the session is gone. Files land under
// ~/.local/share/mcphub/logs, one per gateway identity per day; previous days
// are gzip-compressed on rotation and pruned after the retention window.
//
// Every failure mode here is fail-open. A full disk, a permissions problem,
// a race with another process compressing the same file — none of it may
// take down serving, so Write always reports success and problems surface
// once on stderr instead of as errors.
package logsink

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	retentionDays = 14
	dayFormat     = "2006-01-02"
)

// Writer is an io.Writer appending to a per-day log file.
//
// Two gateways with the same identity (two unscoped `mcp serve` processes)
// share a file; appends are O_APPEND single-writes of one log line, so lines
// from both survive, at worst interleaved — acceptable for a debug log, and
// better than one process winning the file.
type Writer struct {
	mu     sync.Mutex
	dir    string
	name   string
	day    string
	f      *os.File
	warned bool
}

// DefaultDir is where gateway logs live unless MCPHUB_LOG_DIR points
// elsewhere. Setting MCPHUB_LOG_DIR=off disables file logging entirely.
func DefaultDir() string {
	if v := os.Getenv("MCPHUB_LOG_DIR"); v != "" {
		if strings.EqualFold(v, "off") {
			return ""
		}
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "mcphub", "logs")
}

// New opens today's log file for the given identity ("gateway",
// "gateway-sonar") and starts background maintenance of the directory.
func New(dir, name string) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("file logging disabled (no log directory)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, name: name}
	w.mu.Lock()
	err := w.rotateLocked(time.Now())
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}
	go w.maintain()
	return w, nil
}

// Write appends p to today's file, rolling over when the write crosses
// midnight. It never returns an error: the gateway's logging must not become
// the gateway's problem.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if d := now.Format(dayFormat); d != w.day {
		if err := w.rotateLocked(now); err != nil {
			w.warnOnceLocked(err)
			return len(p), nil
		}
		go w.maintain()
	}
	if w.f == nil {
		return len(p), nil
	}
	if _, err := w.f.Write(p); err != nil {
		w.warnOnceLocked(err)
	}
	return len(p), nil
}

// Close closes the current file. Compression of the final file is left to the
// next gateway's maintenance pass — closing must be fast during shutdown.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Path returns the file currently written to, for tests and diagnostics.
func (w *Writer) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return filepath.Join(w.dir, w.name+"-"+w.day+".log")
}

func (w *Writer) rotateLocked(now time.Time) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	w.day = now.Format(dayFormat)
	f, err := os.OpenFile(
		filepath.Join(w.dir, w.name+"-"+w.day+".log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return err
	}
	w.f = f
	return nil
}

func (w *Writer) warnOnceLocked(err error) {
	if w.warned {
		return
	}
	w.warned = true
	fmt.Fprintf(os.Stderr, "mcphub: log file sink degraded (logging continues on stderr only): %v\n", err)
}

// maintain compresses previous days' .log files and prunes anything older
// than the retention window. Errors are skipped file by file: another
// process may be maintaining the same directory concurrently, and losing a
// race is not a problem worth reporting.
func (w *Writer) maintain() {
	w.mu.Lock()
	dir, today := w.dir, w.day
	w.mu.Unlock()
	maintainDir(dir, today, time.Now())
}

func maintainDir(dir, today string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(dir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		switch {
		case info.ModTime().Before(cutoff) &&
			(strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")):
			_ = os.Remove(full)
		case strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, today+".log"):
			compressFile(full)
		}
	}
}

// compressFile gzips src to src.gz via a temp file and removes the original.
// Any failure leaves the original in place for the next pass.
func compressFile(src string) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(src), ".gz-tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	gz := gzip.NewWriter(tmp)
	if _, err := io.Copy(gz, in); err != nil {
		_ = tmp.Close()
		return
	}
	if err := gz.Close(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), src+".gz"); err != nil {
		return
	}
	_ = os.Remove(src)
}
