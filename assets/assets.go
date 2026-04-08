package assets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Manager extracts embedded assets to temporary directories for shell execution.
type Manager struct {
	dies      fs.FS
	preCommit fs.FS
	dataDir   string // lazily created temp dir for pre-commit assets
	scripts   []string
}

// NewManager creates a Manager backed by the given embedded filesystems.
// dies should be rooted at the dies/ directory (or an embed.FS containing it).
// preCommit should be rooted at the pre-commit/ directory.
func NewManager(dies, preCommit fs.FS) *Manager {
	return &Manager{dies: dies, preCommit: preCommit}
}

// DiesFS returns a sub-filesystem containing only dies.
// If the FS contains a top-level "dies" directory, it returns a sub-FS rooted there.
// Otherwise returns the FS as-is (already rooted at dies/).
func (m *Manager) DiesFS() (fs.FS, error) {
	sub, err := fs.Sub(m.dies, "dies")
	if err != nil {
		return m.dies, nil
	}
	return sub, nil
}

// ExtractScript writes a die script from the embedded FS to a temp file
// and returns the path. The file is cleaned up by Cleanup().
func (m *Manager) ExtractScript(diePath string) (string, error) {
	diesFS, err := m.DiesFS()
	if err != nil {
		return "", fmt.Errorf("accessing dies filesystem: %w", err)
	}

	data, err := fs.ReadFile(diesFS, diePath)
	if err != nil {
		return "", fmt.Errorf("reading embedded die %s: %w", diePath, err)
	}

	tmpFile, err := os.CreateTemp("", "forge-die-*.sh")
	if err != nil {
		return "", fmt.Errorf("creating temp script: %w", err)
	}
	tmpPath := tmpFile.Name()

	_, writeErr := tmpFile.Write(data)
	closeErr := tmpFile.Close()
	if writeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("writing temp script: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("closing temp script: %w", closeErr)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp script: %w", err)
	}

	m.scripts = append(m.scripts, tmpPath)
	return tmpPath, nil
}

// DataDir extracts the pre-commit assets (blocks, configs, scripts) to a temp
// directory and returns the path. Subsequent calls return the same path.
// The directory is cleaned up by Cleanup().
func (m *Manager) DataDir() (string, error) {
	if m.dataDir != "" {
		return m.dataDir, nil
	}

	tmpDir, err := os.MkdirTemp("", "forge-data-*")
	if err != nil {
		return "", fmt.Errorf("creating temp data dir: %w", err)
	}

	preCommitFS, err := fs.Sub(m.preCommit, "pre-commit")
	if err != nil {
		preCommitFS = m.preCommit
	}

	destDir := filepath.Join(tmpDir, "pre-commit")
	if err := extractTree(preCommitFS, destDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extracting pre-commit assets: %w", err)
	}

	m.dataDir = tmpDir
	return tmpDir, nil
}

// Cleanup removes all temporary files and directories created by this manager.
func (m *Manager) Cleanup() {
	for _, path := range m.scripts {
		_ = os.Remove(path)
	}
	m.scripts = nil

	if m.dataDir != "" {
		_ = os.RemoveAll(m.dataDir)
		m.dataDir = ""
	}
}

// extractTree walks an fs.FS and writes all files to destDir, preserving
// the directory structure. Scripts (.sh, .py) are made executable.
func extractTree(fsys fs.FS, destDir string) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		destPath := filepath.Join(destDir, path)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		perm := os.FileMode(0o644)
		ext := filepath.Ext(path)
		if ext == ".sh" || ext == ".py" {
			perm = 0o755
		}

		return os.WriteFile(destPath, data, perm)
	})
}
