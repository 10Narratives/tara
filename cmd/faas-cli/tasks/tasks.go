package taskcmd

import "github.com/spf13/cobra"

func NewTaskGroup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Commands for task managing",
	}

	cmd.AddCommand(
		NewGetTaskCmd(),
		NewListTasksCmd(),
		NewCancelTaskCmd(),
		NewDeleteTaskCmd(),
	)

	return cmd
}
