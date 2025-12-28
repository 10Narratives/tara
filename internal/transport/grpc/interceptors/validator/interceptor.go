package validator

import (
	v "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/validator"
	"google.golang.org/grpc"
)

func NewUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return v.UnaryServerInterceptor()
}
