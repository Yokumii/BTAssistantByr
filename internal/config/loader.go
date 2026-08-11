package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"go.yaml.in/yaml/v3"
)

const (
	maxConfigSize = 1 << 20
	gib           = int64(1 << 30)
)

var (
	// ErrPathRequired indicates that no configuration file path was supplied.
	ErrPathRequired = errors.New("configuration path is required")
	// ErrReadConfig indicates that the configuration file could not be read completely.
	ErrReadConfig = errors.New("read configuration")
	// ErrConfigTooLarge indicates that the configuration exceeds the supported size.
	ErrConfigTooLarge = errors.New("configuration exceeds 1 MiB")
	// ErrInvalidConfig indicates that the YAML or a configuration field is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")
)

type yamlConfig struct {
	Tracker   yamlTracker   `yaml:"tracker"`
	Client    yamlClient    `yaml:"client"`
	Strategy  yamlStrategy  `yaml:"strategy"`
	Execution yamlExecution `yaml:"execution"`
	Runtime   yamlRuntime   `yaml:"runtime"`
}

type yamlTracker struct {
	BaseURL  string `yaml:"base_url"`
	ProxyURL string `yaml:"proxy_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Cookie   string `yaml:"cookie"`
}

type yamlClient struct {
	BaseURL  string `yaml:"base_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	SavePath string `yaml:"save_path"`
}

type yamlStrategy struct {
	MaxDiskUsageGiB    int64  `yaml:"max_disk_usage_gib"`
	MaxTorrentSizeGiB  *int64 `yaml:"max_torrent_size_gib"`
	MinFreeSpaceGiB    int64  `yaml:"min_free_space_gib"`
	RefreshInterval    string `yaml:"refresh_interval"`
	QueueCheckInterval string `yaml:"queue_check_interval"`
}

type yamlExecution struct {
	Mode string `yaml:"mode"`
}

type yamlRuntime struct {
	DataDir           string `yaml:"data_dir"`
	LogDir            string `yaml:"log_dir"`
	LogLevel          string `yaml:"log_level"`
	LogColor          string `yaml:"log_color"`
	NetworkRetryCount *int   `yaml:"network_retry_count"`
}

// Load reads one strict YAML document from path and validates all runtime settings.
func Load(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, ErrPathRequired
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrReadConfig, err)
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrReadConfig, err)
	}
	if len(contents) > maxConfigSize {
		return Config{}, ErrConfigTooLarge
	}

	var raw yamlConfig
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		return Config{}, fmt.Errorf("%w: multiple YAML documents are not supported", ErrInvalidConfig)
	}

	return validate(raw)
}

func validate(raw yamlConfig) (Config, error) {
	raw.Tracker.BaseURL = strings.TrimSpace(raw.Tracker.BaseURL)
	raw.Tracker.ProxyURL = strings.TrimSpace(raw.Tracker.ProxyURL)
	raw.Tracker.Username = strings.TrimSpace(raw.Tracker.Username)
	raw.Tracker.Cookie = strings.TrimSpace(raw.Tracker.Cookie)
	raw.Client.BaseURL = strings.TrimSpace(raw.Client.BaseURL)
	raw.Client.Username = strings.TrimSpace(raw.Client.Username)
	raw.Client.SavePath = strings.TrimSpace(raw.Client.SavePath)
	raw.Strategy.RefreshInterval = strings.TrimSpace(raw.Strategy.RefreshInterval)
	raw.Strategy.QueueCheckInterval = strings.TrimSpace(raw.Strategy.QueueCheckInterval)
	raw.Execution.Mode = strings.ToLower(strings.TrimSpace(raw.Execution.Mode))
	raw.Runtime.DataDir = strings.TrimSpace(raw.Runtime.DataDir)
	raw.Runtime.LogDir = strings.TrimSpace(raw.Runtime.LogDir)
	raw.Runtime.LogLevel = strings.ToLower(strings.TrimSpace(raw.Runtime.LogLevel))
	raw.Runtime.LogColor = strings.ToLower(strings.TrimSpace(raw.Runtime.LogColor))

	for _, required := range []struct {
		name  string
		value string
	}{
		{name: "tracker.base_url", value: raw.Tracker.BaseURL},
		{name: "tracker.username", value: raw.Tracker.Username},
		{name: "tracker.password", value: strings.TrimSpace(raw.Tracker.Password)},
		{name: "client.base_url", value: raw.Client.BaseURL},
		{name: "client.username", value: raw.Client.Username},
		{name: "client.password", value: strings.TrimSpace(raw.Client.Password)},
		{name: "strategy.refresh_interval", value: raw.Strategy.RefreshInterval},
		{name: "strategy.queue_check_interval", value: raw.Strategy.QueueCheckInterval},
		{name: "execution.mode", value: raw.Execution.Mode},
		{name: "runtime.data_dir", value: raw.Runtime.DataDir},
		{name: "runtime.log_dir", value: raw.Runtime.LogDir},
		{name: "runtime.log_level", value: raw.Runtime.LogLevel},
		{name: "runtime.log_color", value: raw.Runtime.LogColor},
	} {
		if required.value == "" {
			return Config{}, fieldError(required.name, "is required")
		}
	}
	if err := validateBaseURL("tracker.base_url", raw.Tracker.BaseURL); err != nil {
		return Config{}, err
	}
	if err := validateProxyURL(raw.Tracker.ProxyURL); err != nil {
		return Config{}, err
	}
	if err := validateBaseURL("client.base_url", raw.Client.BaseURL); err != nil {
		return Config{}, err
	}
	if raw.Strategy.MaxDiskUsageGiB <= 0 {
		return Config{}, fieldError("strategy.max_disk_usage_gib", "must be positive")
	}
	if raw.Strategy.MaxDiskUsageGiB > math.MaxInt64/gib {
		return Config{}, fieldError("strategy.max_disk_usage_gib", "overflows bytes")
	}
	maxTorrentSizeBytes := int64(0)
	if raw.Strategy.MaxTorrentSizeGiB != nil {
		if *raw.Strategy.MaxTorrentSizeGiB <= 0 {
			return Config{}, fieldError("strategy.max_torrent_size_gib", "must be positive when configured")
		}
		if *raw.Strategy.MaxTorrentSizeGiB > math.MaxInt64/gib {
			return Config{}, fieldError("strategy.max_torrent_size_gib", "overflows bytes")
		}
		maxTorrentSizeBytes = *raw.Strategy.MaxTorrentSizeGiB * gib
	}
	if raw.Strategy.MinFreeSpaceGiB <= 0 {
		return Config{}, fieldError("strategy.min_free_space_gib", "must be positive")
	}
	if raw.Strategy.MinFreeSpaceGiB > math.MaxInt64/gib {
		return Config{}, fieldError("strategy.min_free_space_gib", "overflows bytes")
	}
	refreshInterval, err := time.ParseDuration(raw.Strategy.RefreshInterval)
	if err != nil || refreshInterval <= 0 {
		return Config{}, fieldError("strategy.refresh_interval", "must be a positive Go duration")
	}
	queueCheckInterval, err := time.ParseDuration(raw.Strategy.QueueCheckInterval)
	if err != nil || queueCheckInterval <= 0 {
		return Config{}, fieldError("strategy.queue_check_interval", "must be a positive Go duration")
	}
	if raw.Runtime.NetworkRetryCount == nil {
		return Config{}, fieldError("runtime.network_retry_count", "is required")
	}
	if *raw.Runtime.NetworkRetryCount < 0 || *raw.Runtime.NetworkRetryCount > 100 {
		return Config{}, fieldError("runtime.network_retry_count", "must be between 0 and 100")
	}
	executionMode := domain.ExecutionMode(raw.Execution.Mode)
	if !executionMode.Valid() {
		return Config{}, fieldError("execution.mode", "must be request_approval or yolo")
	}
	if !oneOf(raw.Runtime.LogLevel, "debug", "info", "warn", "error") {
		return Config{}, fieldError("runtime.log_level", "must be debug, info, warn, or error")
	}
	if !oneOf(raw.Runtime.LogColor, "auto", "always", "never") {
		return Config{}, fieldError("runtime.log_color", "must be auto, always, or never")
	}

	return Config{
		Tracker: Tracker{
			BaseURL:  raw.Tracker.BaseURL,
			ProxyURL: raw.Tracker.ProxyURL,
			Username: raw.Tracker.Username,
			Password: raw.Tracker.Password,
			Cookie:   raw.Tracker.Cookie,
		},
		Client: Client{
			BaseURL:  raw.Client.BaseURL,
			Username: raw.Client.Username,
			Password: raw.Client.Password,
			SavePath: raw.Client.SavePath,
		},
		Strategy: Strategy{
			MaxDiskUsageBytes:   raw.Strategy.MaxDiskUsageGiB * gib,
			MaxTorrentSizeBytes: maxTorrentSizeBytes,
			MinFreeSpaceBytes:   raw.Strategy.MinFreeSpaceGiB * gib,
			RefreshInterval:     refreshInterval,
			QueueCheckInterval:  queueCheckInterval,
		},
		Execution: Execution{Mode: executionMode},
		Runtime: Runtime{
			DataDir:           raw.Runtime.DataDir,
			LogDir:            raw.Runtime.LogDir,
			LogLevel:          raw.Runtime.LogLevel,
			LogColor:          raw.Runtime.LogColor,
			NetworkRetryCount: *raw.Runtime.NetworkRetryCount,
		},
	}, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateBaseURL(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fieldError(field, "must be an HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fieldError(field, "must not contain credentials, query, or fragment")
	}
	return nil
}

func validateProxyURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fieldError("tracker.proxy_url", "is invalid")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fieldError("tracker.proxy_url", "uses an unsupported scheme")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fieldError("tracker.proxy_url", "must not contain a path, query, or fragment")
	}
	return nil
}

func fieldError(field, message string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidConfig, field, message)
}
