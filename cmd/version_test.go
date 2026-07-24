package cmd

import "testing"

// Module versions below are the real shapes debug.ReadBuildInfo() reports for each
// install path — a goreleaser binary, `go install pkg@latest`, and a local `go build`.
func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name           string
		ldflagsVersion string
		moduleVersion  string
		want           string
	}{
		{"goreleaser ldflags win", "1.6.0", "(devel)", "1.6.0"},
		{"go install stamps module version", "dev", "v1.3.0", "1.3.0"},
		{"local go build is a dev build", "dev", "v1.6.1-0.20260724161156-2c0470373d7d+dirty", "dev"},
		{"clean local go build is a dev build", "dev", "(devel)", "dev"},
		{"no build info version", "dev", "", "dev"},
		{"prerelease tag is not a release", "dev", "v1.6.0-rc1", "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveVersion(tt.ldflagsVersion, tt.moduleVersion)
			if got != tt.want {
				t.Errorf("resolveVersion(%q, %q) = %q, want %q",
					tt.ldflagsVersion, tt.moduleVersion, got, tt.want)
			}
		})
	}
}
