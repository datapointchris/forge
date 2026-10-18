package timewindow

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, newYork)
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, newYork) }

	cases := []struct {
		name, since, until string
		want               Window
		refused            bool
	}{
		{name: "neither flag", want: Window{}},
		{name: "seconds", since: "45s", want: Window{Since: now.Add(-45 * time.Second)}},
		{name: "m is minutes", since: "30m", want: Window{Since: now.Add(-30 * time.Minute)}},
		{name: "hours", since: "12h", want: Window{Since: now.Add(-12 * time.Hour)}},
		{name: "a day is elapsed, not midnight", since: "1d", want: Window{Since: at(2026, 10, 17, 9)}},
		{name: "weeks", since: "2w", want: Window{Since: now.Add(-14 * 24 * time.Hour)}},
		{name: "a day on since is its first instant", since: "2026-10-01", want: Window{Since: at(2026, 10, 1, 0)}},
		{name: "a day on until includes all of it", until: "2026-10-07", want: Window{Until: at(2026, 10, 8, 0)}},
		{name: "a day the clock shifts on is built, not added", until: "2026-03-08", want: Window{Until: at(2026, 3, 9, 0)}},
		{
			name: "an instant", since: "2026-10-01T14:00:00-04:00", until: "2026-10-02T00:00:00Z",
			want: Window{Since: at(2026, 10, 1, 14), Until: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
		},
		{name: "a span on until", since: "3d", until: "1d", want: Window{Since: at(2026, 10, 15, 9), Until: at(2026, 10, 17, 9)}},
		{name: "no month unit", since: "6mo", refused: true},
		{name: "no year unit", since: "1y", refused: true},
		{name: "no words", since: "2 weeks", refused: true},
		{name: "no fraction", since: "1.5d", refused: true},
		{name: "no bare number", since: "72", refused: true},
		{name: "no Go duration", since: "1h30m", refused: true},
		{name: "no impossible day", until: "2026-10-32", refused: true},
		{name: "too long to count", since: "99999999999w", refused: true},
		{name: "since after until", since: "1d", until: "2d", refused: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.since, c.until, now)
			if c.refused {
				if err == nil {
					t.Fatalf("Parse(%q, %q) = %v, want a refusal", c.since, c.until, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q, %q): %v", c.since, c.until, err)
			}
			if !got.Since.Equal(c.want.Since) || !got.Until.Equal(c.want.Until) {
				t.Errorf("Parse(%q, %q) = %v to %v, want %v to %v", c.since, c.until, got.Since, got.Until, c.want.Since, c.want.Until)
			}
		})
	}
}
