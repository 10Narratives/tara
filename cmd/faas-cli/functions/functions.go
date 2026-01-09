package funccmd

import (
	"github.com/spf13/cobra"
)

func NewFunctionsGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "funcs",
		Short: "Commands for managing serverless functions",
	}

	cmd.AddCommand(
		NewUploadFunctionCmd(),
		NewGetFunctionCmd(),
		NewListFunctionsCmd(),
		NewDeleteFunctionCmd(),
		NewExecuteFunctionCmd(),
	)

	return cmd
}
