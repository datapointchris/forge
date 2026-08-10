package main

import "github.com/datapointchris/forge/cmd"

func main() {
	cmd.SetEmbeddedAssets(embeddedPreCommit, embeddedCI)
	cmd.Execute()
}
