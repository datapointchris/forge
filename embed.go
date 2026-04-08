package main

import "embed"

//go:embed dies/registry.yml dies/checks dies/maintenance dies/onetime
var embeddedDies embed.FS

//go:embed pre-commit
var embeddedPreCommit embed.FS
