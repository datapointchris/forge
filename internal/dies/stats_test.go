package dies

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAndLoadStats(t *testing.T) {
	dir := t.TempDir()
	statsPath := filepath.Join(dir, "stats.jsonl")

	record := RunRecord{
		Die:       "maintenance/fix.sh",
		Timestamp: time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC),
		Results: map[string]string{
			"repo-a": "OK",
			"repo-b": "SKIP (not found)",
		},
		OK:   1,
		Skip: 1,
		Fail: 0,
	}

	if err := RecordRun(statsPath, record); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	records, err := LoadStats(statsPath)
	if err != nil {
		t.Fatalf("LoadStats: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	got := records[0]
	if got.Die != "maintenance/fix.sh" {
		t.Errorf("Die = %q, want %q", got.Die, "maintenance/fix.sh")
	}
	if got.OK != 1 || got.Skip != 1 || got.Fail != 0 {
		t.Errorf("counts = %d/%d/%d, want 1/1/0", got.OK, got.Skip, got.Fail)
	}
	if got.Results["repo-a"] != "OK" {
		t.Errorf("repo-a = %q, want OK", got.Results["repo-a"])
	}
}

func TestLoadStatsMultipleRecords(t *testing.T) {
	dir := t.TempDir()
	statsPath := filepath.Join(dir, "stats.jsonl")

	for i := range 3 {
		if err := RecordRun(statsPath, RunRecord{
			Die:       "checks/lint.sh",
			Timestamp: time.Now().Add(time.Duration(i) * time.Hour),
			Results:   map[string]string{"repo": "OK"},
			OK:        1,
		}); err != nil {
			t.Fatalf("RecordRun[%d]: %v", i, err)
		}
	}

	records, err := LoadStats(statsPath)
	if err != nil {
		t.Fatalf("LoadStats: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("got %d records, want 3", len(records))
	}
}

func TestLoadStatsMissingFile(t *testing.T) {
	records, err := LoadStats("/nonexistent/stats.jsonl")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if records != nil {
		t.Errorf("expected nil records, got %d", len(records))
	}
}

func TestLoadStatsSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	statsPath := filepath.Join(dir, "stats.jsonl")

	content := `{"die":"good.sh","timestamp":"2026-03-31T10:00:00Z","results":{},"ok":1,"skip":0,"fail":0}
this is not json
{"die":"also-good.sh","timestamp":"2026-03-31T11:00:00Z","results":{},"ok":2,"skip":0,"fail":0}
`
	if err := os.WriteFile(statsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := LoadStats(statsPath)
	if err != nil {
		t.Fatalf("LoadStats: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("got %d records, want 2 (should skip malformed line)", len(records))
	}
}

func TestStatsForDie(t *testing.T) {
	records := []RunRecord{
		{Die: "a.sh", OK: 1},
		{Die: "b.sh", OK: 2},
		{Die: "a.sh", OK: 3},
	}

	filtered := StatsForDie(records, "a.sh")
	if len(filtered) != 2 {
		t.Errorf("got %d, want 2", len(filtered))
	}
}

func TestRecordRunCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	statsPath := filepath.Join(dir, "nested", "deep", "stats.jsonl")

	err := RecordRun(statsPath, RunRecord{Die: "test.sh"})
	if err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	if _, err := os.Stat(statsPath); err != nil {
		t.Errorf("stats file not created: %v", err)
	}
}
