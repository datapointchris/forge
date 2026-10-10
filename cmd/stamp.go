package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/forge/dies"
	"github.com/datapointchris/forge/toolchain"
)

var stampCmd = goclikit.AsNamespace(&cobra.Command{
	Use:   "stamp",
	Short: "The version stamp forge writes into every file it generates",
	Long: `Every file forge generates from the version declaration opens with a stamp
naming the declaration's version, which is how a rollout tells a repo on the
current standard from one behind it.

  spec    which files carry the stamp, and its exact form

A tool reading stamps takes both from here at run time, so the installed forge
is the one authority on where stamps live.`,
})

var stampSpecJSON bool

var stampSpecCmd = &cobra.Command{
	Use:   "spec",
	Short: "Print which files carry the stamp, and its exact form",
	Long: `Print the stamp's prefix, the line it sits on, and each file a die writes it
into with the die that writes it. The version follows the prefix as a
decimal number. A path is relative to a repo's root.`,
	Example: "  forge stamp spec\n  forge stamp spec --json",
	Args:    cobra.NoArgs,
	RunE:    runStampSpec,
}

// stampSpec is the whole of what a reader needs to find a stamp and name the
// verb that moves it.
type stampSpec struct {
	Prefix string             `json:"prefix"`
	Line   int                `json:"line"`
	Files  []dies.StampedFile `json:"files"`
}

func init() {
	stampSpecCmd.Flags().BoolVar(&stampSpecJSON, "json", false, "output as JSON to stdout")
	stampCmd.AddCommand(stampSpecCmd)
	rootCmd.AddCommand(stampCmd)
}

func runStampSpec(cmd *cobra.Command, _ []string) error {
	spec := stampSpec{
		Prefix: toolchain.StampPrefix,
		Line:   toolchain.StampLine,
		Files:  dies.StampedFiles(),
	}
	out := cmd.OutOrStdout()

	if stampSpecJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(spec)
	}

	row(out, "prefix %q on line %d, then the version\n\n", spec.Prefix, spec.Line)
	table := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	for _, file := range spec.Files {
		if _, err := fmt.Fprintf(table, "%s\t%s\n", file.Path, file.Die); err != nil {
			return err
		}
	}
	return table.Flush()
}
