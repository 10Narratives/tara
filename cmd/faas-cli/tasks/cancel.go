package taskcmd

import (
	"context"
	"fmt"
	"time"

	faaspb "github.com/10Narratives/faas/pkg/faas/v1"
	"github.com/spf13/cobra"
)

func NewCancelTaskCmd() *cobra.Command {
	var (
		taskName    string
		gatewayAddr string
		tls         bool
		caFile      string
		timeout     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel task",
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskName == "" {
				return fmt.Errorf("--name is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			conn, err := dialGateway(ctx, gatewayAddr, tls, caFile)
			if err != nil {
				return err
			}
			defer conn.Close()

			client := faaspb.NewTasksClient(conn)
			t, err := client.CancelTask(ctx, &faaspb.CancelTaskRequest{
				Name: taskName,
			})
			if err != nil {
				return err
			}

			createdAt := ""
			if ts := t.GetCreatedAt(); ts != nil {
				createdAt = ts.AsTime().Format(time.RFC3339Nano)
			}

			startedAt := ""
			if ts := t.GetStartedAt(); ts != nil {
				startedAt = ts.AsTime().Format(time.RFC3339Nano)
			}

			endedAt := ""
			if ts := t.GetEndedAt(); ts != nil {
				endedAt = ts.AsTime().Format(time.RFC3339Nano)
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"task: name=%s, state=%s, created_at=%s, started_at=%s, ended_at=%s\n",
				t.GetName(),
				t.GetState().String(),
				createdAt,
				startedAt,
				endedAt,
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&taskName, "name", "", "Task name, e.g. tasks/my-task")
	cmd.Flags().StringVar(&gatewayAddr, "gateway", "127.0.0.1:55055", "Gateway gRPC address host:port")
	cmd.Flags().BoolVar(&tls, "tls", false, "Use TLS")
	cmd.Flags().StringVar(&caFile, "tls-ca", "", "CA file (PEM), optional")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "Overall timeout")

	return cmd
}
