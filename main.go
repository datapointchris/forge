package main

import "github.com/datapointchris/forge/v3/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedDies, embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
