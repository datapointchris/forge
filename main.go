package main

import "github.com/datapointchris/forge/v5/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
