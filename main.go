package main

import "github.com/datapointchris/forge/v4/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedDies, embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
