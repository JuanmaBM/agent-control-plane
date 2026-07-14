// Package gateway implements subcommands for interacting with openshell gateways.
package gateway

import (
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage openshell gateways",
	Long: `Manage openshell gateways.

Examples:
  acpctl gateway setup <name>    # configure openshell CLI access for a gateway`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	Cmd.AddCommand(setupCmd)
}
