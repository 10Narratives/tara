package gatewayapp

type Config struct {
	Transport TransportConfig `yaml:"transport"`
}

type TransportConfig struct {
	Grpc GrpcConfig `yaml:"grpc"`
}

type GrpcConfig struct {
	Address string `yaml:"address"`
}
