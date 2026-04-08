package dies

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
