// Package timewindow reads a read's `--since` and `--until` into two instants.
//
// Each bound is a span back from now (30m, 12h, 7d, 2w), a day read in the
// machine's zone, or an RFC 3339 instant, which is cli-design.md's rule for a
// read bounded by time. Every forge read bounded by time resolves its flags
// here, so one spelling means one instant on every command.
package timewindow

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// Window is the stretch of time a read keeps.
type Window struct {
	// Since is the first instant inside the window. The zero time is the
	// earliest row.
	Since time.Time
	// Until is the first instant past the window. The zero time is no bound.
	Until time.Time
}

// Contains reports whether t falls inside the window.
func (w Window) Contains(t time.Time) bool {
	return (w.Since.IsZero() || !t.Before(w.Since)) && (w.Until.IsZero() || t.Before(w.Until))
}

// span is an integer and one unit. Months and years are refused, because
// neither has a fixed length.
var span = regexp.MustCompile(`^(\d+)([smhdw])$`)

var units = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
	"w": 7 * 24 * time.Hour,
}

// Parse resolves the text of --since and --until against now. An empty text
// leaves its bound open.
func Parse(since, until string, now time.Time) (Window, error) {
	var w Window
	var err error
	if since != "" {
		if w.Since, err = bound("since", since, now, false); err != nil {
			return Window{}, err
		}
	}
	if until != "" {
		if w.Until, err = bound("until", until, now, true); err != nil {
			return Window{}, err
		}
	}
	if !w.Since.IsZero() && !w.Until.IsZero() && !w.Since.Before(w.Until) {
		return Window{}, fmt.Errorf("--since %s does not start before --until %s, so nothing is between them", since, until)
	}
	return w, nil
}

// bound reads one flag. A day on --until is the next day's first instant, so
// the whole day is inside. Both days are built with time.Date in now's zone,
// never reached by adding hours, so a day a clock shifts on keeps its length.
func bound(flag, text string, now time.Time, isEnd bool) (time.Time, error) {
	if m := span.FindStringSubmatch(text); m != nil {
		unit := units[m[2]]
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || n > math.MaxInt64/int64(unit) {
			return time.Time{}, fmt.Errorf("--%s %s is longer than a span can count; write a day instead", flag, text)
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	if day, err := time.ParseInLocation(time.DateOnly, text, now.Location()); err == nil {
		if isEnd {
			return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, now.Location()), nil
		}
		return day, nil
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(
		"--%s %q is not a span (30m, 12h, 7d, 2w), a day (2026-10-01) or an RFC 3339 instant (2026-10-01T14:00:00-04:00)",
		flag, text)
}
