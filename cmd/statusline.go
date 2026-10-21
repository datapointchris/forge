package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"golang.org/x/sys/unix"

	"github.com/datapointchris/forge/reconcile"
)

// statusLine is the line under a walk's rows saying how many targets are
// measured and which are still running. It is drawn only where stderr is a
// terminal, because a line rewritten in place is noise in a log.
type statusLine struct {
	w        io.Writer
	fd       int
	enabled  bool
	total    int
	noun     string
	measured int
	running  []string
	drawn    bool
}

func newStatusLine(w io.Writer, total int, noun string) *statusLine {
	s := &statusLine{w: w, total: total, noun: noun}
	if f, ok := w.(*os.File); ok && isatty.IsTerminal(f.Fd()) {
		s.enabled = true
		s.fd = int(f.Fd())
	}
	return s
}

func (s *statusLine) watch(measured func([]reconcile.Measurement)) reconcile.Watch {
	return reconcile.Watch{
		Started: func(t reconcile.Target) {
			s.running = append(s.running, t.Repo.Name)
			s.draw()
		},
		Finished: func(t reconcile.Target) {
			s.running = slices.DeleteFunc(s.running, func(name string) bool { return name == t.Repo.Name })
			s.measured++
			s.draw()
		},
		Measured: func(ms []reconcile.Measurement) {
			s.clear()
			measured(ms)
			s.draw()
		},
	}
}

func (s *statusLine) draw() {
	if !s.enabled {
		return
	}
	line := fmt.Sprintf("%d of %d %s measured", s.measured, s.total, s.noun)
	if len(s.running) > 0 {
		line += " · running " + strings.Join(s.running, ", ")
	}
	// A line wider than the terminal wraps, and the carriage return then
	// clears only its last row.
	if ws, err := unix.IoctlGetWinsize(s.fd, unix.TIOCGWINSZ); err == nil && ws.Col > 1 {
		if runes := []rune(line); len(runes) >= int(ws.Col) {
			line = string(runes[:ws.Col-1])
		}
	}
	_, _ = fmt.Fprint(s.w, "\r\033[K"+color.New(color.Faint).Sprint(line))
	s.drawn = true
}

func (s *statusLine) clear() {
	if !s.drawn {
		return
	}
	_, _ = fmt.Fprint(s.w, "\r\033[K")
	s.drawn = false
}
