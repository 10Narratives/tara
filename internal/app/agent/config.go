package agentapp

type Config struct {
	UnifiedStorage UnifiedStorageConfig `yaml:"unified_storage"`
}

type UnifiedStorageConfig struct {
	URL string `yaml:"url" env-required:"true"`
}
