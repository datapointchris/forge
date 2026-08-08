package dies

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const DefaultStatsPath = "~/.local/share/forge/stats.jsonl"

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

func StatsForDie(records []RunRecord, diePath string) []RunRecord {
	var filtered []RunRecord
	for _, r := range records {
		if r.Die == diePath {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// sinceUnits maps a normalized unit to the instant that many of them before t.
// Months and years go through AddDate rather than a fixed multiple of hours,
// so "3 months" lands on the calendar date a reader would name.
var sinceUnits = map[string]func(t time.Time, n int) time.Time{
	"minute": func(t time.Time, n int) time.Time { return t.Add(-time.Duration(n) * time.Minute) },
	"hour":   func(t time.Time, n int) time.Time { return t.Add(-time.Duration(n) * time.Hour) },
	"day":    func(t time.Time, n int) time.Time { return t.AddDate(0, 0, -n) },
	"week":   func(t time.Time, n int) time.Time { return t.AddDate(0, 0, -7*n) },
	"month":  func(t time.Time, n int) time.Time { return t.AddDate(0, -n, 0) },
	"year":   func(t time.Time, n int) time.Time { return t.AddDate(-n, 0, 0) },
}

var sinceAliases = map[string]string{
	"min": "minute",
	"h":   "hour", "hr": "hour",
	"d": "day", "w": "week", "mo": "month", "y": "year",
}

var sinceCountUnit = regexp.MustCompile(`^([0-9]+)\s*([a-z]+)$`)

// ParseSince resolves a window spec to the instant it starts at. It accepts an
// ISO date (2026-07-01), a Go duration (72h), and git log's own spelling
// ("2 weeks", "30 days ago", "2.weeks.ago") — the last because this flag gets
// typed in the same breath as the git command it was learned from.
//
// "m" is deliberately not an alias: git reads it as minutes and half the world
// reads it as months, and a window silently 43000x wrong is worse than an error.
func ParseSince(spec string, now time.Time) (time.Time, error) {
	trimmed := strings.ToLower(strings.TrimSpace(spec))
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty --since value")
	}

	if date, err := time.ParseInLocation("2006-01-02", trimmed, now.Location()); err == nil {
		return date, nil
	}

	// Go durations are parsed before the dot is treated as a git-style separator,
	// or 1.5h would normalize to "1 5h" and come back as fifteen hours.
	if d, err := time.ParseDuration(trimmed); err == nil {
		if d < 0 {
			return time.Time{}, fmt.Errorf("--since must name a window in the past: %q", spec)
		}
		return now.Add(-d), nil
	}

	normalized := strings.ReplaceAll(trimmed, ".", " ")
	normalized = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(normalized), "ago"))
	normalized = strings.Join(strings.Fields(normalized), " ")

	match := sinceCountUnit.FindStringSubmatch(normalized)
	if match == nil {
		return time.Time{}, fmt.Errorf("unrecognized --since value %q: try \"2 weeks\", \"30 days\", \"72h\", or 2026-07-01", spec)
	}

	count, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("unrecognized --since count in %q", spec)
	}

	unit := strings.TrimSuffix(match[2], "s")
	if alias, ok := sinceAliases[unit]; ok {
		unit = alias
	}
	shift, ok := sinceUnits[unit]
	if !ok {
		return time.Time{}, fmt.Errorf("unrecognized --since unit %q: use hours, days, weeks, months, or years", match[2])
	}

	return shift(now, count), nil
}

// Since returns the records at or after cutoff, preserving order.
func Since(records []RunRecord, cutoff time.Time) []RunRecord {
	var kept []RunRecord
	for _, r := range records {
		if !r.Timestamp.Before(cutoff) {
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
