package main

import "github.com/datapointchris/forge/v5/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedDies, embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
