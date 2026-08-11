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
	MaxDiskUsageBytes int64
	RefreshInterval   time.Duration
}

// Execution contains the write-safety policy.
type Execution struct {
	Mode domain.ExecutionMode
}

// Runtime contains paths used for generated application data.
type Runtime struct {
	DataDir  string
	LogDir   string
	LogLevel string
	LogColor string
}
