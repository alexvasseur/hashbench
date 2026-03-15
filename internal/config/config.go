package config

import (
	"errors"
	"time"
)

const (
	DefaultAddr                  = "127.0.0.1:6379"
	DefaultThreads               = 8
	DefaultClients               = 8
	DefaultKeys                  = 10000
	DefaultValueBytes            = 16
	DefaultWriteRatio            = 0.5
	DefaultRequests       uint64 = 0
	DefaultDuration              = 10 * time.Second
	DefaultPipeline              = 1
	DefaultSeed           int64  = 0
	DefaultReportInterval        = 5 * time.Second
	DefaultKeyPattern            = "sequential"
)

// Config holds all benchmark parameters.
type Config struct {
	Addr           string
	Password       string
	DB             int
	TLS            bool
	Cluster        bool
	Threads        int
	Clients        int
	Keys           int
	ValueBytes     int
	WriteRatio     float64
	LoadOnly       bool
	RunRatio       string
	Requests       uint64
	Duration       time.Duration
	Pipeline       int
	Seed           int64
	ReportInterval time.Duration
	KeyPattern     string
	JSON           bool
}

func (c *Config) Validate() error {
	if c.Threads <= 0 {
		return errors.New("threads must be > 0")
	}
	if c.Clients <= 0 {
		return errors.New("client must be > 0")
	}
	if c.Keys <= 0 {
		return errors.New("keys must be > 0")
	}
	if c.ValueBytes <= 0 {
		return errors.New("value-bytes must be >= 1")
	}
	if c.WriteRatio < 0 || c.WriteRatio > 1 {
		return errors.New("write-ratio must be in [0,1]")
	}
	if c.Duration < 0 {
		return errors.New("duration must be >= 0")
	}
	if c.Pipeline <= 0 {
		return errors.New("pipeline must be >= 1")
	}
	switch c.KeyPattern {
	case "random", "sequential":
		// ok
	default:
		return errors.New("key-pattern must be random or sequential")
	}
	return nil
}

// StopConditionMet returns true when either duration has elapsed or the requests limit is reached.
func StopConditionMet(total uint64, requests uint64, durationReached bool) bool {
	if durationReached {
		return true
	}
	if requests > 0 && total >= requests {
		return true
	}
	return false
}
