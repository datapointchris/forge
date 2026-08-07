package main

import "github.com/datapointchris/forge/v2/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedDies, embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
