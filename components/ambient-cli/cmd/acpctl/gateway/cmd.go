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
  acpctl gateway setup-cli     # configure openshell CLI access for a gateway
  acpctl gateway remove-cli    # remove an openshell CLI gateway registration`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	Cmd.AddCommand(setupCmd)
	Cmd.AddCommand(removeCmd)
}
