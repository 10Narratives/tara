package reflectapi

import (
	grpctr "github.com/10Narratives/faas/internal/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func NewRegistration() grpctr.ServiceRegistration {
	return func(s *grpc.Server) {
		reflection.Register(s)
	}
}
