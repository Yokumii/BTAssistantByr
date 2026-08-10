// Package config defines and loads validated application configuration.
package config

import "time"

// Config contains all application configuration groups.
type Config struct {
	Tracker  Tracker
	Client   Client
	Strategy Strategy
	Runtime  Runtime
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

// Runtime contains paths used for generated application data.
type Runtime struct {
	DataDir string
	LogDir  string
}
