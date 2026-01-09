package taskcmd

import (
	"context"
	"fmt"
	"time"

	faaspb "github.com/10Narratives/faas/pkg/faas/v1"
	"github.com/spf13/cobra"
)

func NewListTasksCmd() *cobra.Command {
	var (
		gatewayAddr string
		tls         bool
		caFile      string
		timeout     time.Duration

		pageSize  int32
		pageToken string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			conn, err := dialGateway(ctx, gatewayAddr, tls, caFile)
			if err != nil {
				return err
			}
			defer conn.Close()

			client := faaspb.NewTasksClient(conn)
			resp, err := client.ListTasks(ctx, &faaspb.ListTasksRequest{
				PageSize:  pageSize,
				PageToken: pageToken,
			})
			if err != nil {
				return err
			}

			for _, t := range resp.GetTasks() {
				if t == nil {
					continue
				}

				createdAt := ""
				if ts := t.GetCreatedAt(); ts != nil {
					createdAt = ts.AsTime().Format(time.RFC3339Nano)
				}

				fmt.Fprintf(cmd.OutOrStdout(),
					"task: name=%s, function=%s, state=%s, created_at=%s\n",
					t.GetName(),
					t.GetFunction(),
					t.GetState().String(),
					createdAt,
				)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "next_page_token=%s\n", resp.GetNextPageToken())
			return nil
		},
	}

	cmd.Flags().StringVar(&gatewayAddr, "gateway", "127.0.0.1:55055", "Gateway gRPC address host:port")
	cmd.Flags().BoolVar(&tls, "tls", false, "Use TLS")
	cmd.Flags().StringVar(&caFile, "tls-ca", "", "CA file (PEM), optional")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "Overall timeout")

	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Max number of tasks to return (0 = server default)")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Pagination token from previous response")

	return cmd
}
