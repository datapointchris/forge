package dies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/forge/reconcile"
)

// fakeGH puts a gh on PATH that identifies the repo and answers the protection
// read the way the API refused it, with git linked beside it.
func fakeGH(t *testing.T, refusal string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	linkTool(t, bin, "git")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"repo) echo 'datapointchris/thing main' ;;\n" +
		"api) echo '" + refusal + "' >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func assessProtection(t *testing.T, refusal string) reconcile.Measurement {
	t.Helper()
	fakeGH(t, refusal)
	target := fixture(t, stacks("go"), nil)
	withRemote(t, target, "git@github.com:datapointchris/thing.git")
	return reconcile.Assess(target, BranchProtection{})
}

func TestBranchProtectionReportsAnUnprotectedBranchAsDriftApplyFixes(t *testing.T) {
	measured := assessProtection(t, "gh: Branch not protected (HTTP 404)")

	if len(measured.Changes) != 1 {
		t.Fatalf("changes = %v, want 1", measured.Changes)
	}
	change := measured.Changes[0]
	if change.Verdict != reconcile.Missing || change.Repair != reconcile.Automatic {
		t.Errorf("verdict, repair = %q, %q; want missing, automatic", change.Verdict, change.Repair)
	}
}

// A free plan answers 403 for any private repo, and no write changes that, so
// reporting it as drift would hold every private repo's plan at exit 1 for good.
func TestBranchProtectionTreatsAPlanWithoutProtectionAsOutOfScope(t *testing.T) {
	measured := assessProtection(t,
		"gh: Upgrade to GitHub Pro or make this repository public to enable this feature. (HTTP 403)")

	if len(measured.Changes) != 0 {
		t.Errorf("changes = %v, want none", measured.Changes)
	}
	if measured.Summary == "" {
		t.Error("summary is empty, want it to say why there is nothing to do")
	}
	if got := measured.Fold(reconcile.LensPlan); got.Status != reconcile.Converged {
		t.Errorf("status = %q, want converged", got.Status)
	}
}

func TestBranchProtectionReportsAnUnreadableAnswerAsUnmeasured(t *testing.T) {
	for _, refusal := range []string{
		"gh: Server Error (HTTP 502)",
		"gh: Resource not accessible by integration (HTTP 403)",
	} {
		t.Run(refusal, func(t *testing.T) {
			measured := assessProtection(t, refusal)

			if len(measured.Changes) != 1 || measured.Changes[0].Verdict != reconcile.Unknown {
				t.Fatalf("changes = %v, want one unknown", measured.Changes)
			}
			if reconcile.CodeFor([]reconcile.Result{measured.Fold(reconcile.LensPlan)}) != reconcile.ExitConverged {
				t.Error("an unread answer moved the exit code")
			}
		})
	}
}
