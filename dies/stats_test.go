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

func TestSortNewestFirst(t *testing.T) {
	records := []RunRecord{
		{Die: "a.sh", Timestamp: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
		{Die: "c.sh", Timestamp: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{Die: "b.sh", Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
	}

	SortNewestFirst(records)

	want := []string{"c.sh", "b.sh", "a.sh"}
	for i, name := range want {
		if records[i].Die != name {
			t.Errorf("position %d = %q, want %q", i, records[i].Die, name)
		}
	}
}

func TestAggregateByDieTotalsAndOrdersByRecency(t *testing.T) {
	records := []RunRecord{
		{Die: "old.sh", Timestamp: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), OK: 3, Skip: 1},
		{Die: "busy.sh", Timestamp: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), OK: 2, Fail: 1},
		{Die: "busy.sh", Timestamp: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), OK: 4, Skip: 2},
	}

	aggregated := AggregateByDie(records)
	if len(aggregated) != 2 {
		t.Fatalf("got %d rows, want one per die", len(aggregated))
	}

	// The die run most recently leads, whatever order the append-only log holds.
	if aggregated[0].Die != "busy.sh" {
		t.Errorf("first row = %q, want busy.sh", aggregated[0].Die)
	}
	if aggregated[0].Runs != 2 {
		t.Errorf("Runs = %d, want 2", aggregated[0].Runs)
	}
	if aggregated[0].OK != 6 || aggregated[0].Skip != 2 || aggregated[0].Fail != 1 {
		t.Errorf("totals = %d/%d/%d, want 6/2/1", aggregated[0].OK, aggregated[0].Skip, aggregated[0].Fail)
	}
	if !aggregated[0].LastRun.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("LastRun = %s, want the later of the two runs", aggregated[0].LastRun)
	}
}

func TestAggregateByDieIsDeterministicOnTiedTimestamps(t *testing.T) {
	stamp := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	records := []RunRecord{
		{Die: "zebra.sh", Timestamp: stamp},
		{Die: "alpha.sh", Timestamp: stamp},
	}

	for range 5 {
		aggregated := AggregateByDie(records)
		if aggregated[0].Die != "alpha.sh" {
			t.Fatalf("map iteration order leaked into the output: got %q first", aggregated[0].Die)
		}
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
