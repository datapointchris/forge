package main

import "embed"

//go:embed dies
var embeddedDies embed.FS

//go:embed pre-commit
var embeddedPreCommit embed.FS
