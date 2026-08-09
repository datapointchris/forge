package main

import "embed"

//go:embed dies/registry.yml dies/checks dies/maintenance
var embeddedDies embed.FS

//go:embed pre-commit
var embeddedPreCommit embed.FS

//go:embed ci/blocks
var embeddedCI embed.FS
