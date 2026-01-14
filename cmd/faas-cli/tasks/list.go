package taskcmd

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"time"

	faaspb "github.com/10Narratives/faas/pkg/faas/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func NewListTasksCmd() *cobra.Command {
	var (
		gatewayAddr string
		tls         bool
		caFile      string
		timeout     time.Duration

		pageSize  int32
		pageToken string

		all bool

		csvPath         string
		csvInlineBase64 bool
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

			if csvPath != "" {
				return listTasksCSV(ctx, cmd.OutOrStdout(), client, pageSize, pageToken, all, csvPath, csvInlineBase64)
			}

			return listTasksText(ctx, cmd.OutOrStdout(), client, pageSize, pageToken, all)
		},
	}

	cmd.Flags().StringVar(&gatewayAddr, "gateway", "127.0.0.1:55055", "Gateway gRPC address host:port")
	cmd.Flags().BoolVar(&tls, "tls", false, "Use TLS")
	cmd.Flags().StringVar(&caFile, "tls-ca", "", "CA file (PEM), optional")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "Overall timeout")

	cmd.Flags().Int32Var(&pageSize, "page-size", 0, "Max number of tasks to return (0 = server default)")
	cmd.Flags().StringVar(&pageToken, "page-token", "", "Pagination token from previous response")

	cmd.Flags().BoolVar(&all, "all", false, "Iterate through all pages")

	cmd.Flags().StringVar(&csvPath, "csv", "", "Write tasks to CSV file (use '-' for stdout)")
	cmd.Flags().BoolVar(&csvInlineBase64, "csv-inline-base64", false, "Include inline_result as base64 in CSV (may be large)")

	return cmd
}

func listTasksText(
	ctx context.Context,
	out io.Writer,
	client faaspb.TasksClient,
	pageSize int32,
	pageToken string,
	all bool,
) error {
	if !all {
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

			fmt.Fprintf(out,
				"task: name=%s, function=%s, state=%s, created_at=%s\n",
				t.GetName(),
				t.GetFunction(),
				t.GetState().String(),
				formatTS(t.GetCreatedAt()),
			)
		}

		fmt.Fprintf(out, "next_page_token=%s\n", resp.GetNextPageToken())
		return nil
	}

	token := pageToken
	for {
		resp, err := client.ListTasks(ctx, &faaspb.ListTasksRequest{
			PageSize:  pageSize,
			PageToken: token,
		})
		if err != nil {
			return err
		}

		for _, t := range resp.GetTasks() {
			if t == nil {
				continue
			}

			fmt.Fprintf(out,
				"task: name=%s, function=%s, state=%s, created_at=%s\n",
				t.GetName(),
				t.GetFunction(),
				t.GetState().String(),
				formatTS(t.GetCreatedAt()),
			)
		}

		token = resp.GetNextPageToken()
		if token == "" {
			break
		}
	}

	return nil
}

func listTasksCSV(
	ctx context.Context,
	defaultOut io.Writer,
	client faaspb.TasksClient,
	pageSize int32,
	pageToken string,
	all bool,
	csvPath string,
	csvInlineBase64 bool,
) error {
	var out io.Writer = defaultOut
	var f *os.File
	var err error

	if csvPath != "-" {
		f, err = os.Create(csvPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}

	w := csv.NewWriter(out)
	defer w.Flush()

	if err := w.Write([]string{
		"name",
		"function",
		"parameters",
		"state",
		"created_at",
		"started_at",
		"ended_at",
		"result_type",
		"inline_result_len",
		"inline_result_base64",
		"object_key",
		"error_message",
	}); err != nil {
		return err
	}

	writeTasks := func(tasks []*faaspb.Task) error {
		for _, t := range tasks {
			if t == nil {
				continue
			}

			resultType := ""
			inlineLen := ""
			inlineB64 := ""
			objectKey := ""
			errorMsg := ""

			if r := t.GetResult(); r != nil {
				switch x := r.Data.(type) {
				case *faaspb.TaskResult_InlineResult:
					resultType = "inline_result"
					inlineLen = fmt.Sprintf("%d", len(x.InlineResult))
					if csvInlineBase64 && len(x.InlineResult) > 0 {
						inlineB64 = base64.StdEncoding.EncodeToString(x.InlineResult)
					}
				case *faaspb.TaskResult_ObjectKey:
					resultType = "object_key"
					objectKey = x.ObjectKey
				case *faaspb.TaskResult_ErrorMessage:
					resultType = "error_message"
					errorMsg = x.ErrorMessage
				default:
				}
			}

			if err := w.Write([]string{
				t.GetName(),
				t.GetFunction(),
				t.GetParameters(),
				t.GetState().String(),
				formatTS(t.GetCreatedAt()),
				formatTS(t.GetStartedAt()),
				formatTS(t.GetEndedAt()),
				resultType,
				inlineLen,
				inlineB64,
				objectKey,
				errorMsg,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if !all {
		resp, err := client.ListTasks(ctx, &faaspb.ListTasksRequest{
			PageSize:  pageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return err
		}
		if err := writeTasks(resp.GetTasks()); err != nil {
			return err
		}
		if err := w.Error(); err != nil {
			return err
		}
		return nil
	}

	token := pageToken
	for {
		resp, err := client.ListTasks(ctx, &faaspb.ListTasksRequest{
			PageSize:  pageSize,
			PageToken: token,
		})
		if err != nil {
			return err
		}

		if err := writeTasks(resp.GetTasks()); err != nil {
			return err
		}

		token = resp.GetNextPageToken()
		if token == "" {
			break
		}
	}

	if err := w.Error(); err != nil {
		return err
	}
	return nil
}

func formatTS(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	if !ts.IsValid() {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}
