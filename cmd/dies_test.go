package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/datapointchris/goclikit"

	"github.com/datapointchris/forge/dies"
)

// A die name in `dies stats` is refused when it names no die and no recorded
// run, and accepted when it names either, so a renamed die's history stays
// reachable under the name it ran as.
func TestDiesStatsRefusesANameThatMatchesNothing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	statsPath, err := dies.StatsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := dies.RecordRun(statsPath, dies.RunRecord{Die: "renamed-die", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	if err := runLine(t, "dies", "stats", "no-such-die"); !errors.Is(err, goclikit.ErrUsage) {
		t.Errorf("dies stats no-such-die = %v, want a usage error", err)
	}
	for _, name := range []string{"precommit", "renamed-die"} {
		if err := runLine(t, "dies", "stats", name); err != nil {
			t.Errorf("dies stats %s = %v, want it accepted", name, err)
		}
	}
}
