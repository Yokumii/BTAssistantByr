// Package config defines and loads validated application configuration.
package config

import (
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

// Config contains all application configuration groups.
type Config struct {
	Tracker   Tracker
	Client    Client
	Strategy  Strategy
	Execution Execution
	Runtime   Runtime
	Resend    *Resend
}

// Resend enables execution emails only when explicitly configured.
type Resend struct {
	APIKey  string
	From    string
	To      []string
	Timeout time.Duration
}

// Tracker contains BYR connection settings.
type Tracker struct {
	BaseURL  string
	ProxyURL string
	Username string
	Password string
	Cookie   string
}

// Client contains torrent client connection settings.
type Client struct {
	BaseURL  string
	Username string
	Password string
	SavePath string
}

// Strategy contains TopFree strategy settings.
type Strategy struct {
	// Priority is free_time (the documented default) or bonus.
	Priority            string
	MaxDiskUsageBytes   int64
	MaxTorrentSizeBytes int64
	MinFreeSpaceBytes   int64
	RefreshInterval     time.Duration
	QueueCheckInterval  time.Duration
}

// Execution contains the write-safety policy.
type Execution struct {
	Mode domain.ExecutionMode
}

const (
	DefaultLogMaxSizeMiB = int64(20)
	DefaultLogMaxBackups = 5
)

// Runtime contains paths and logging limits for application data.
type Runtime struct {
	DataDir  string
	LogDir   string
	LogLevel string
	LogColor string
	// LogMaxSizeBytes and LogMaxBackups default to 20 MiB and 5 in Load.
	LogMaxSizeBytes   int64
	LogMaxBackups     int
	NetworkRetryCount int
}
