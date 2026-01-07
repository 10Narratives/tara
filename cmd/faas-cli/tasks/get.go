package taskcmd

import (
	"context"
	"fmt"
	"time"

	faaspb "github.com/10Narratives/faas/pkg/faas/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func dialGateway(ctx context.Context, addr string, tls bool, caFile string) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if tls {
		creds, err := credentials.NewClientTLSFromFile(caFile, "")
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	opts = append(opts, grpc.WithBlock())

	return grpc.DialContext(ctx, addr, opts...)
}

func NewGetTaskCmd() *cobra.Command {
	var (
		taskName    string
		gatewayAddr string
		tls         bool
		caFile      string
		timeout     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get task metadata",
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
			t, err := client.GetTask(ctx, &faaspb.GetTaskRequest{
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

			resultType := ""
			resultValue := ""
			if r := t.GetResult(); r != nil {
				switch v := r.GetData().(type) {
				case *faaspb.TaskResult_InlineResult:
					resultType = "inline"
					resultValue = string(v.InlineResult)
				case *faaspb.TaskResult_ObjectKey:
					resultType = "object_key"
					resultValue = v.ObjectKey
				case *faaspb.TaskResult_ErrorMessage:
					resultType = "error"
					resultValue = v.ErrorMessage
				default:
					resultType = "unknown"
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"task: name=%s, function=%s, state=%s, created_at=%s, started_at=%s, ended_at=%s, parameters=%s, result_type=%s, result=%s\n",
				t.GetName(),
				t.GetFunction(),
				t.GetState().String(),
				createdAt,
				startedAt,
				endedAt,
				t.GetParameters(),
				resultType,
				resultValue,
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
