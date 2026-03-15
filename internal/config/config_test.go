package config

import (
	"testing"
	"time"
)

func baseConfig() Config {
	return Config{
		Addr:           DefaultAddr,
		Threads:        DefaultThreads,
		Clients:        DefaultClients,
		Keys:           DefaultKeys,
		ValueBytes:     DefaultValueBytes,
		WriteRatio:     DefaultWriteRatio,
		LoadOnly:       false,
		RunRatio:       "",
		Requests:       DefaultRequests,
		Duration:       DefaultDuration,
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

func TestValidateWriteRatio(t *testing.T) {
	cfg := baseConfig()
	cfg.WriteRatio = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for write-ratio out of range")
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

func TestValidateDuration(t *testing.T) {
	cfg := baseConfig()
	cfg.Duration = -1 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for negative duration")
	}
}

func TestStopConditionMet(t *testing.T) {
	if !StopConditionMet(10, 0, true) {
		t.Fatalf("duration should stop")
	}
	if StopConditionMet(5, 10, false) {
		t.Fatalf("should not stop before requests reached")
	}
	if !StopConditionMet(10, 10, false) {
		t.Fatalf("should stop when requests reached")
	}
}
