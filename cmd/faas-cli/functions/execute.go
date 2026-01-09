package funccmd

import (
	"context"
	"fmt"
	"time"

	faaspb "github.com/10Narratives/faas/pkg/faas/v1"
	"github.com/spf13/cobra"
)

func NewExecuteFunctionCmd() *cobra.Command {
	var (
		functionName string
		gatewayAddr  string
		tls          bool
		caFile       string
		timeout      time.Duration

		parameters string
	)

	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Execute function (create task)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if functionName == "" {
				return fmt.Errorf("--name is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			conn, err := dialGateway(ctx, gatewayAddr, tls, caFile)
			if err != nil {
				return err
			}
			defer conn.Close()

			client := faaspb.NewFunctionsClient(conn)
			resp, err := client.ExecuteFunction(ctx, &faaspb.ExecuteFunctionRequest{
				Name:       functionName,
				Parameters: parameters,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "task: name=%s\n", resp.GetName())
			return nil
		},
	}

	cmd.Flags().StringVar(&functionName, "name", "", "Function name, e.g. functions/my-func")
	cmd.Flags().StringVar(&gatewayAddr, "gateway", "127.0.0.1:55055", "Gateway gRPC address host:port")
	cmd.Flags().BoolVar(&tls, "tls", false, "Use TLS")
	cmd.Flags().StringVar(&caFile, "tls-ca", "", "CA file (PEM), optional")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "Overall timeout")

	cmd.Flags().StringVar(&parameters, "params", "", "Execute parameters as string (format is application-specific)")

	return cmd
}
