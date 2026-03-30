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
	DefaultRequests       uint64 = 0
	DefaultQPS                   = 0
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
	LoadOnly       bool
	RunRatio       string
	Requests       uint64
	Conn           int
	QPS            int
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
	if c.Conn < 0 {
		return errors.New("conn must be >= 0")
	}
	if c.QPS < 0 {
		return errors.New("qps must be >= 0")
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
