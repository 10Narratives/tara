package funcapi_test

import (
	"context"
	"io"
	"testing"
	"time"

	funcapi "github.com/10Narratives/faas/internal/transport/grpc/api/functions"
	"github.com/10Narratives/faas/internal/transport/grpc/api/functions/mocks"
	faaspb "github.com/10Narratives/faas/pkg/faas/v1"

	funcdomain "github.com/10Narratives/faas/internal/domains/functions"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ---- fake stream for UploadFunction ----

type fakeUploadStream struct {
	ctx context.Context

	reqs []*faaspb.UploadFunctionRequest
	i    int

	sendCalled bool
	sent       *faaspb.Function
	sendErr    error
}

func (s *fakeUploadStream) Recv() (*faaspb.UploadFunctionRequest, error) {
	if s.i >= len(s.reqs) {
		return nil, io.EOF
	}
	r := s.reqs[s.i]
	s.i++
	return r, nil
}

func (s *fakeUploadStream) SendAndClose(res *faaspb.Function) error {
	s.sendCalled = true
	s.sent = res
	return s.sendErr
}

// grpc.ServerStream methods (для компиляции; в хендлере не используются)
func (s *fakeUploadStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeUploadStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeUploadStream) SetTrailer(metadata.MD)       {}
func (s *fakeUploadStream) Context() context.Context     { return s.ctx }
func (s *fakeUploadStream) SendMsg(any) error            { return nil }
func (s *fakeUploadStream) RecvMsg(any) error            { return nil }

// ---- tests ----

func TestUploadFunction_MissingMetadata(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	stream := &fakeUploadStream{
		ctx:  context.Background(),
		reqs: nil, // сразу EOF
	}

	err := s.UploadFunction(stream)
	st := status.Convert(err)

	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "missing upload metadata")
	require.False(t, stream.sendCalled)
}

func TestUploadFunction_FirstMessageNotMetadata(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	stream := &fakeUploadStream{
		ctx: context.Background(),
		reqs: []*faaspb.UploadFunctionRequest{
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionData{
					UploadFunctionData: &faaspb.UploadFunctionData{Data: []byte("x")},
				},
			},
		},
	}

	err := s.UploadFunction(stream)
	st := status.Convert(err)

	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "first message must be upload_function_metadata")
	require.False(t, stream.sendCalled)
}

func TestUploadFunction_InvalidName(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	stream := &fakeUploadStream{
		ctx: context.Background(),
		reqs: []*faaspb.UploadFunctionRequest{
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionMetadata{
					UploadFunctionMetadata: &faaspb.UploadFunctionMetadata{
						FunctionName: "bad-name", // not functions/{...}
						Format:       faaspb.UploadFunctionMetadata_FORMAT_ZIP,
					},
				},
			},
		},
	}

	err := s.UploadFunction(stream)
	st := status.Convert(err)

	require.Equal(t, codes.InvalidArgument, st.Code())
	require.False(t, stream.sendCalled)
}

func TestUploadFunction_MetadataTwice(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	// Важно: хендлер делает <-done перед возвратом ошибки,
	// поэтому мок UploadFunction должен завершиться (не блокироваться на чтении forever).
	svc.EXPECT().
		UploadFunction(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, args *funcdomain.UploadFunctionArgs) {
			_, _ = io.ReadAll(args.Data) // вернется после CloseWithError в хендлере
		}).
		Return(&funcdomain.UploadFunctionResult{
			Function: &funcdomain.Function{
				InternalID: uuid.New(),
				Name:       funcdomain.FunctionName("functions/foo"),
				UploadedAt: time.Now(),
			},
		}, nil).
		Once()

	stream := &fakeUploadStream{
		ctx: context.Background(),
		reqs: []*faaspb.UploadFunctionRequest{
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionMetadata{
					UploadFunctionMetadata: &faaspb.UploadFunctionMetadata{
						FunctionName: "functions/foo",
						Format:       faaspb.UploadFunctionMetadata_FORMAT_ZIP,
					},
				},
			},
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionMetadata{
					UploadFunctionMetadata: &faaspb.UploadFunctionMetadata{
						FunctionName: "functions/foo",
						Format:       faaspb.UploadFunctionMetadata_FORMAT_ZIP,
					},
				},
			},
		},
	}

	err := s.UploadFunction(stream)
	st := status.Convert(err)

	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "upload_function_metadata must be sent only once")
	require.False(t, stream.sendCalled)
}

func TestUploadFunction_Success_StreamsBodyToDomain(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	uploadedAt := time.Now().UTC()

	domainFn := &funcdomain.Function{
		InternalID:  uuid.New(),
		Name:        funcdomain.FunctionName("functions/foo"),
		DisplayName: "Foo",
		UploadedAt:  uploadedAt,
		Bundle: &funcdomain.SourceBundle{
			Bucket:    "bucket-1",
			ObjectKey: "obj-1",
			Size:      11,
			SHA256:    "abcd",
		},
	}

	want := []byte("hello world")

	svc.EXPECT().
		UploadFunction(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, args *funcdomain.UploadFunctionArgs) {
			require.Equal(t, funcdomain.FunctionName("functions/foo"), args.Name)
			require.Equal(t, funcdomain.ZipFormat, args.Format)

			b, err := io.ReadAll(args.Data)
			require.NoError(t, err)
			require.Equal(t, want, b)
		}).
		Return(&funcdomain.UploadFunctionResult{Function: domainFn}, nil).
		Once()

	stream := &fakeUploadStream{
		ctx: context.Background(),
		reqs: []*faaspb.UploadFunctionRequest{
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionMetadata{
					UploadFunctionMetadata: &faaspb.UploadFunctionMetadata{
						FunctionName: "functions/foo",
						Format:       faaspb.UploadFunctionMetadata_FORMAT_ZIP,
					},
				},
			},
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionData{
					UploadFunctionData: &faaspb.UploadFunctionData{Data: []byte("hello ")},
				},
			},
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionData{
					UploadFunctionData: &faaspb.UploadFunctionData{Data: []byte("world")},
				},
			},
		},
	}

	err := s.UploadFunction(stream)
	require.NoError(t, err)

	require.True(t, stream.sendCalled)
	require.NotNil(t, stream.sent)
	require.Equal(t, "functions/foo", stream.sent.GetName())
	require.Equal(t, "Foo", stream.sent.GetDisplayName())
	require.True(t, stream.sent.GetUploadedAt().AsTime().Equal(uploadedAt))
	require.Equal(t, "bucket-1", stream.sent.GetSourceBundle().GetBucket())
	require.Equal(t, "obj-1", stream.sent.GetSourceBundle().GetObjectKey())
	require.Equal(t, uint64(11), stream.sent.GetSourceBundle().GetSize())
	require.Equal(t, "abcd", stream.sent.GetSourceBundle().GetSha256())
}

func TestUploadFunction_DomainReturnsAlreadyExists(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	svc.EXPECT().
		UploadFunction(mock.Anything, mock.Anything).
		Run(func(ctx context.Context, args *funcdomain.UploadFunctionArgs) {
			_, _ = io.ReadAll(args.Data)
		}).
		Return((*funcdomain.UploadFunctionResult)(nil), funcdomain.ErrFunctionAlreadyExists).
		Once()

	stream := &fakeUploadStream{
		ctx: context.Background(),
		reqs: []*faaspb.UploadFunctionRequest{
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionMetadata{
					UploadFunctionMetadata: &faaspb.UploadFunctionMetadata{
						FunctionName: "functions/foo",
						Format:       faaspb.UploadFunctionMetadata_FORMAT_ZIP,
					},
				},
			},
			{
				Payload: &faaspb.UploadFunctionRequest_UploadFunctionData{
					UploadFunctionData: &faaspb.UploadFunctionData{Data: []byte("x")},
				},
			},
		},
	}

	err := s.UploadFunction(stream)
	st := status.Convert(err)

	require.Equal(t, codes.AlreadyExists, st.Code())
	require.False(t, stream.sendCalled)
}

func TestGetFunction_NilFunction_Internal(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	svc.EXPECT().
		GetFunction(mock.Anything, mock.Anything).
		Return(&funcdomain.GetFunctionResult{Function: nil}, nil).
		Once()

	_, err := s.GetFunction(context.Background(), &faaspb.GetFunctionRequest{Name: "functions/foo"})
	st := status.Convert(err)

	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "missing function in result")
}

func TestListFunctions_SkipsNil(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	f1 := &funcdomain.Function{
		InternalID:  uuid.New(),
		Name:        funcdomain.FunctionName("functions/a"),
		DisplayName: "A",
		UploadedAt:  time.Now(),
		Bundle:      &funcdomain.SourceBundle{Bucket: "b", ObjectKey: "k"},
	}

	svc.EXPECT().
		ListFunctions(mock.Anything, mock.Anything).
		Return(&funcdomain.ListFunctionsResult{
			Functions:     []*funcdomain.Function{nil, f1},
			NextPageToken: "next",
		}, nil).
		Once()

	resp, err := s.ListFunctions(context.Background(), &faaspb.ListFunctionsRequest{
		PageSize:  10,
		PageToken: "pt",
	})
	require.NoError(t, err)
	require.Equal(t, "next", resp.GetNextPageToken())
	require.Len(t, resp.GetFunctions(), 1)
	require.Equal(t, "functions/a", resp.GetFunctions()[0].GetName())
}

func TestDeleteFunction_NotFound(t *testing.T) {
	svc := mocks.NewFunctionService(t)
	s := funcapi.NewServer(svc)

	svc.EXPECT().
		DeleteFunction(mock.Anything, mock.Anything).
		Return(funcdomain.ErrFunctionNotFound).
		Once()

	_, err := s.DeleteFunction(context.Background(), &faaspb.DeleteFunctionRequest{Name: "functions/missing"})
	st := status.Convert(err)

	require.Equal(t, codes.NotFound, st.Code())
}
