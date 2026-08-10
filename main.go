package main

import "github.com/datapointchris/forge/v6/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
