package dies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/config"
	"github.com/datapointchris/forge/reconcile"
)

func TestOpenBaoLoginWritesAnExecutableScriptThatThenReadsAsCurrent(t *testing.T) {
	ctx := t.Context()
	target := fixture(t, &config.Toolchain{OpenBaoLogin: true}, nil)

	applyAll(t, target, OpenBaoLogin{})

	want := readFile(t, filepath.Join(repoRoot(t), "ci", "scripts", openBaoLoginAsset))
	if got := readFile(t, target.Path(openBaoLoginPath)); got != want {
		t.Errorf("%s is not the template", openBaoLoginPath)
	}
	info, err := os.Stat(target.Path(openBaoLoginPath))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s has mode %v; a workflow step runs it by path", openBaoLoginPath, info.Mode().Perm())
	}
	if changes := reconcile.Assess(ctx, target, OpenBaoLogin{}).Changes; len(changes) != 0 {
		t.Errorf("the written script reads as drift, so its marker is not recognized: %+v", changes)
	}
}

func TestOpenBaoLoginReportsAnUnmarkedCopyAndLeavesItInPlace(t *testing.T) {
	ctx := t.Context()
	copied := "#!/usr/bin/env bash\n# Runs one command logged in to OpenBao.\n"
	target := fixture(t, &config.Toolchain{OpenBaoLogin: true}, map[string]string{openBaoLoginPath: copied})

	changes := reconcile.Assess(ctx, target, OpenBaoLogin{}).Changes
	if len(changes) != 1 || changes[0].Repair != reconcile.ByHand {
		t.Fatalf("expected one by-hand finding for the unmarked copy, got %+v", changes)
	}
	applyAll(t, target, OpenBaoLogin{})
	if got := readFile(t, target.Path(openBaoLoginPath)); got != copied {
		t.Errorf("apply overwrote a copy forge did not write:\n%s", got)
	}
}
