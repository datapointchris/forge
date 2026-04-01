package main

import "github.com/datapointchris/forge/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedDies, embeddedPreCommit)
	cmd.Execute()
}
