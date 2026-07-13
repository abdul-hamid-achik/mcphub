package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestBackup(t *testing.T) {
	tests := []struct {
		name    string
		files   []string // created oldest-first, one second of mtime apart
		want    string   // "" means an error is expected
		wantErr bool
	}{
		{
			name:    "no backups",
			files:   []string{"config.json"},
			wantErr: true,
		},
		{
			name:  "timestamped backup as written by harness.backup",
			files: []string{"config.json", "config.json.bak-20260713-000000"},
			want:  "config.json.bak-20260713-000000",
		},
		{
			name: "newest of several wins",
			files: []string{
				"config.json.bak-20260713-000000",
				"config.json.bak-20260713-000130",
			},
			want: "config.json.bak-20260713-000130",
		},
		{
			name: "same-second collision suffix wins the mtime tie",
			files: []string{
				"config.json.bak-20260713-000000",
				"config.json.bak-20260713-000000-1",
			},
			want: "config.json.bak-20260713-000000-1",
		},
		{
			name:  "plain .bak still accepted",
			files: []string{"config.json.bak"},
			want:  "config.json.bak",
		},
		{
			name:    "backups of a sibling config are ignored",
			files:   []string{"other.json.bak-20260713-000000"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			base := time.Now().Add(-time.Duration(len(tt.files)) * time.Second)
			for i, f := range tt.files {
				p := filepath.Join(dir, f)
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				mt := base.Add(time.Duration(i) * time.Second)
				if tt.name == "same-second collision suffix wins the mtime tie" {
					mt = base // identical mtimes: the -N name must break the tie
				}
				if err := os.Chtimes(p, mt, mt); err != nil {
					t.Fatal(err)
				}
			}

			got, err := latestBackup(filepath.Join(dir, "config.json"))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(got) != tt.want {
				t.Fatalf("latestBackup = %q, want %q", filepath.Base(got), tt.want)
			}
		})
	}
}
