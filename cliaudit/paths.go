package cliaudit

import (
	"path/filepath"

	"github.com/datapointchris/forge/config"
)

// snapshotDir is where saved surfaces live under forge's own data directory.
const snapshotDir = "cli-surface"

// SnapshotDir resolves the directory for saved surfaces under forge's data
// directory.
//
// clisurface takes the directory as a parameter rather than resolving one, so
// that where surfaces live stays the consumer's decision. This is forge's
// answer.
func SnapshotDir() (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, snapshotDir), nil
}
