package config

import "testing"

func baseConfig() Config {
	return Config{
		Addr:           DefaultAddr,
		Threads:        DefaultThreads,
		Clients:        DefaultClients,
		Keys:           DefaultKeys,
		ValueBytes:     DefaultValueBytes,
		LoadOnly:       false,
		RunRatio:       "",
		Requests:       DefaultRequests,
		QPS:            DefaultQPS,
		Pipeline:       DefaultPipeline,
		Seed:           DefaultSeed,
		ReportInterval: DefaultReportInterval,
		KeyPattern:     DefaultKeyPattern,
	}
}

func TestValidateOK(t *testing.T) {
	cfg := baseConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCounts(t *testing.T) {
	cfg := baseConfig()
	cfg.Threads = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for threads")
	}
	cfg = baseConfig()
	cfg.Clients = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for client")
	}
	cfg = baseConfig()
	cfg.Keys = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for keys")
	}
}
