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

// The reference instant for window math. Fixed so month and year arithmetic is
// asserted against a real calendar rather than whatever today happens to be.
var sinceNow = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func TestParseSinceAcceptedSpellings(t *testing.T) {
	cases := []struct {
		spec string
		want time.Time
	}{
		{"2 weeks", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
		{"30 days", time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)},
		{"30 days ago", time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)},
		{"2.weeks.ago", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
		{"1 month", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)},
		{"1 year", time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)},
		{"3 hours", time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)},
		{"72h", time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)},
		{"3d", time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)},
		{"2w", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
		{"6mo", time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)},
		{"1y", time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)},
		{"2 WEEKS", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
		{"  2 weeks  ", time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)},
		{"2026-07-01", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range cases {
		got, err := ParseSince(tc.spec, sinceNow)
		if err != nil {
			t.Errorf("ParseSince(%q): %v", tc.spec, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("ParseSince(%q) = %s, want %s", tc.spec, got, tc.want)
		}
	}
}

// A dot is a git-style separator in "2.weeks.ago" and a decimal point in a Go
// duration. Normalizing the separator first turned 1.5h into fifteen hours — a
// window silently ten times wrong, which reads as a working flag.
func TestParseSinceKeepsFractionalDurations(t *testing.T) {
	got, err := ParseSince("1.5h", sinceNow)
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	want := time.Date(2026, 8, 8, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("ParseSince(\"1.5h\") = %s, want %s", got, want)
	}
}

func TestParseSinceRejectsUnusable(t *testing.T) {
	// "m" is rejected rather than guessed: git reads it as minutes, most people
	// mean months, and a window 43000x off looks exactly like a working one.
	for _, spec := range []string{"", "   ", "banana", "2 m", "5 fortnights", "weeks", "-3 days"} {
		if got, err := ParseSince(spec, sinceNow); err == nil {
			t.Errorf("ParseSince(%q) = %s, want an error", spec, got)
		}
	}
}

func TestSinceKeepsBoundaryRecord(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	records := []RunRecord{
		{Die: "old.sh", Timestamp: cutoff.Add(-time.Second)},
		{Die: "boundary.sh", Timestamp: cutoff},
		{Die: "new.sh", Timestamp: cutoff.Add(time.Hour)},
	}

	kept := Since(records, cutoff)
	if len(kept) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(kept), kept)
	}
	if kept[0].Die != "boundary.sh" {
		t.Errorf("a record exactly at the cutoff must be inside the window, got %q", kept[0].Die)
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
