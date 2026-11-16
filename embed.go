package main

import "embed"

//go:embed pre-commit
var embeddedPreCommit embed.FS

//go:embed ci/blocks ci/scripts
var embeddedCI embed.FS
