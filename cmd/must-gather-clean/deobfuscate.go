package main

import (
	"github.com/openshift/must-gather-clean/pkg/cli"
	"github.com/spf13/cobra"
)

var (
	deobfuscationMapPath    string
	deobfuscationInputPath  string
	deobfuscationOutputPath string
)

var deobfuscateCmd = &cobra.Command{
	Use:   "deobfuscate",
	Short: "Restore values in a support response using a private map",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return cli.RunDeobfuscate(deobfuscationMapPath, deobfuscationInputPath, deobfuscationOutputPath)
	},
}

func init() {
	flags := deobfuscateCmd.Flags()
	flags.StringVar(&deobfuscationMapPath, "map", "", "The private deobfuscation map")
	flags.StringVarP(&deobfuscationInputPath, "input", "i", "", "The support response file, or stdin when omitted")
	flags.StringVarP(&deobfuscationOutputPath, "output", "o", "", "The output file, or stdout when omitted")
	_ = deobfuscateCmd.MarkFlagRequired("map")
	rootCmd.AddCommand(deobfuscateCmd)
}
