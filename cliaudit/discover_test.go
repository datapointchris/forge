package cliaudit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/config"
)

func TestDiscoverReachesOnlyActiveReposWeOwn(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	repos := []config.Repo{
		{Name: "alpha", Path: t.TempDir(), Binary: "alpha", Status: "active"},
		{Name: "beta", Path: t.TempDir(), Binary: "beta", Status: "dormant"},
		{Name: "gamma", Path: t.TempDir(), Binary: "gamma"},
		{Name: "delta", Path: t.TempDir(), Binary: "delta", Status: "active", Reference: true},
	}
	found, unresolved := Discover(repos)
	if len(found) != 1 || found[0].Repo != "alpha" {
		t.Errorf("found = %+v, want only alpha — an absent status is not active", found)
	}
	if len(unresolved) != 0 {
		t.Errorf("unresolved = %+v, want none", unresolved)
	}
}
