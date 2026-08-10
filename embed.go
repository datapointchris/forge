package main

import "embed"

//go:embed pre-commit
var embeddedPreCommit embed.FS

//go:embed ci/blocks
var embeddedCI embed.FS
