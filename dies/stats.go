package dies

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/timewindow"
)

// StatsPath is the run log under forge's data directory, which `dies stats`
// reads and every check, plan and apply appends to.
func StatsPath() (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stats.jsonl"), nil
}

type RunRecord struct {
	Die       string            `json:"die"`
	Timestamp time.Time         `json:"timestamp"`
	Results   map[string]string `json:"results"`
	OK        int               `json:"ok"`
	Skip      int               `json:"skip"`
	Fail      int               `json:"fail"`
}

func RecordRun(statsPath string, record RunRecord) error {
	dir := filepath.Dir(statsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating stats directory: %w", err)
	}

	f, err := os.OpenFile(statsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening stats file: %w", err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling run record: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing run record: %w", err)
	}

	return nil
}

func LoadStats(statsPath string) ([]RunRecord, error) {
	f, err := os.Open(statsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening stats file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []RunRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec RunRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip malformed lines (crash resilience)
			continue
		}
		records = append(records, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stats: %w", err)
	}

	return records, nil
}

func StatsForDie(records []RunRecord, name string) []RunRecord {
	var filtered []RunRecord
	for _, r := range records {
		if r.Die == name {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// Within returns the records inside the window, preserving order.
func Within(records []RunRecord, window timewindow.Window) []RunRecord {
	var kept []RunRecord
	for _, r := range records {
		if window.Contains(r.Timestamp) {
			kept = append(kept, r)
		}
	}
	return kept
}

// SortNewestFirst orders records most-recent-first, in place. The log is an
// append-only JSONL, so its natural order puts the oldest run on the reader's
// first screen — which is what trained skimming past the whole report.
func SortNewestFirst(records []RunRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})
}

// DieStats totals one die's runs over whatever window it was built from.
type DieStats struct {
	Die     string    `json:"die"`
	Runs    int       `json:"runs"`
	LastRun time.Time `json:"last_run"`
	OK      int       `json:"ok"`
	Skip    int       `json:"skip"`
	Fail    int       `json:"fail"`
}

// AggregateByDie collapses runs to one row per die, most-recently-run first.
// Per-run rows grow without bound while the number of dies does not, so the
// aggregate is what stays readable as the log fills up.
func AggregateByDie(records []RunRecord) []DieStats {
	byDie := make(map[string]*DieStats)
	for _, r := range records {
		s, ok := byDie[r.Die]
		if !ok {
			s = &DieStats{Die: r.Die}
			byDie[r.Die] = s
		}
		s.Runs++
		s.OK += r.OK
		s.Skip += r.Skip
		s.Fail += r.Fail
		if r.Timestamp.After(s.LastRun) {
			s.LastRun = r.Timestamp
		}
	}

	aggregated := make([]DieStats, 0, len(byDie))
	for _, s := range byDie {
		aggregated = append(aggregated, *s)
	}
	sort.Slice(aggregated, func(i, j int) bool {
		if aggregated[i].LastRun.Equal(aggregated[j].LastRun) {
			return aggregated[i].Die < aggregated[j].Die
		}
		return aggregated[i].LastRun.After(aggregated[j].LastRun)
	})
	return aggregated
}

type DieSummary struct {
	RunCount int
	LastRun  time.Time
}

func SummaryByDie(records []RunRecord) map[string]DieSummary {
	summaries := make(map[string]DieSummary)
	for _, r := range records {
		s := summaries[r.Die]
		s.RunCount++
		if r.Timestamp.After(s.LastRun) {
			s.LastRun = r.Timestamp
		}
		summaries[r.Die] = s
	}
	return summaries
}
