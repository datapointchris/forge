package main

import "github.com/datapointchris/forge/v7/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
