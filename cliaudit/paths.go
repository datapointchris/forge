package cliaudit

import (
	"fmt"
	"os"
	"path/filepath"
)

// snapshotDir is where saved surfaces live under forge's own data directory.
const snapshotDir = "cli-surface"

// SnapshotDir resolves forge's XDG data directory for saved surfaces.
//
// clisurface takes the directory as a parameter rather than resolving one, so
// that where surfaces live stays the consumer's decision. This is forge's
// answer, resolved the same way DefaultReposPath does.
func SnapshotDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "forge", snapshotDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "forge", snapshotDir), nil
}
