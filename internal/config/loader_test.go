package config

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

const validYAML = `tracker:
  base_url: "https://byr.pt"
  proxy_url: "socks5://127.0.0.1:10808"
  username: " byr-user "
  password: " tracker password "
  cookie: " existing-cookie "

client:
  base_url: "http://127.0.0.1:8080/webui/"
  username: " qbt-user "
  password: " client password "
  save_path: " /downloads "

strategy:
  max_disk_usage_gib: 500
  refresh_interval: "121s"

execution:
  mode: " REQUEST_APPROVAL "

runtime:
  data_dir: " data "
  log_dir: " logs "
`

func TestLoad(t *testing.T) {
	t.Parallel()

	configuration, err := Load(writeConfig(t, validYAML))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.Tracker.BaseURL != "https://byr.pt" || configuration.Tracker.ProxyURL != "socks5://127.0.0.1:10808" {
		t.Errorf("unexpected tracker URLs: %+v", configuration.Tracker)
	}
	if configuration.Tracker.Username != "byr-user" || configuration.Tracker.Password != " tracker password " || configuration.Tracker.Cookie != "existing-cookie" {
		t.Errorf("unexpected tracker credentials")
	}
	if configuration.Client.BaseURL != "http://127.0.0.1:8080/webui/" || configuration.Client.Username != "qbt-user" || configuration.Client.Password != " client password " || configuration.Client.SavePath != "/downloads" {
		t.Errorf("unexpected client configuration")
	}
	if configuration.Strategy.MaxDiskUsageBytes != 500*(1<<30) || configuration.Strategy.RefreshInterval != 121*time.Second {
		t.Errorf("unexpected strategy configuration: %+v", configuration.Strategy)
	}
	if configuration.Execution.Mode != domain.ExecutionModeRequestApproval {
		t.Errorf("unexpected execution configuration: %+v", configuration.Execution)
	}
	if configuration.Runtime.DataDir != "data" || configuration.Runtime.LogDir != "logs" {
		t.Errorf("unexpected runtime configuration: %+v", configuration.Runtime)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "unknown field", content: strings.Replace(validYAML, "  cookie:", "  unexpected: true\n  cookie:", 1)},
		{name: "invalid YAML", content: "tracker: ["},
		{name: "multiple documents", content: validYAML + "---\ntracker: {}\n"},
		{name: "missing tracker username", content: strings.Replace(validYAML, `username: " byr-user "`, `username: ""`, 1)},
		{name: "missing tracker password", content: strings.Replace(validYAML, `password: " tracker password "`, `password: ""`, 1)},
		{name: "blank tracker password", content: strings.Replace(validYAML, `password: " tracker password "`, `password: "   "`, 1)},
		{name: "invalid tracker URL", content: strings.Replace(validYAML, "https://byr.pt", "byr.pt", 1)},
		{name: "tracker URL credentials", content: strings.Replace(validYAML, "https://byr.pt", "https://user@byr.pt", 1)},
		{name: "unsupported proxy", content: strings.Replace(validYAML, "socks5://127.0.0.1:10808", "ftp://127.0.0.1:10808", 1)},
		{name: "proxy path", content: strings.Replace(validYAML, "socks5://127.0.0.1:10808", "http://127.0.0.1:10808/proxy", 1)},
		{name: "invalid client URL", content: strings.Replace(validYAML, "http://127.0.0.1:8080/webui/", "http://127.0.0.1:8080?token=value", 1)},
		{name: "missing client username", content: strings.Replace(validYAML, `username: " qbt-user "`, `username: ""`, 1)},
		{name: "missing client password", content: strings.Replace(validYAML, `password: " client password "`, `password: ""`, 1)},
		{name: "zero disk limit", content: strings.Replace(validYAML, "max_disk_usage_gib: 500", "max_disk_usage_gib: 0", 1)},
		{name: "disk limit overflow", content: strings.Replace(validYAML, "max_disk_usage_gib: 500", "max_disk_usage_gib: 8589934592", 1)},
		{name: "invalid interval", content: strings.Replace(validYAML, `refresh_interval: "121s"`, `refresh_interval: "later"`, 1)},
		{name: "zero interval", content: strings.Replace(validYAML, `refresh_interval: "121s"`, `refresh_interval: "0s"`, 1)},
		{name: "missing execution mode", content: strings.Replace(validYAML, `mode: " REQUEST_APPROVAL "`, `mode: ""`, 1)},
		{name: "invalid execution mode", content: strings.Replace(validYAML, `mode: " REQUEST_APPROVAL "`, `mode: "dry_run"`, 1)},
		{name: "missing data directory", content: strings.Replace(validYAML, `data_dir: " data "`, `data_dir: ""`, 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(writeConfig(t, test.content))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Load() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestLoadAcceptsYOLOExecutionMode(t *testing.T) {
	t.Parallel()

	content := strings.Replace(validYAML, `mode: " REQUEST_APPROVAL "`, `mode: "YOLO"`, 1)
	configuration, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.Execution.Mode != domain.ExecutionModeYOLO {
		t.Errorf("execution mode = %q, want %q", configuration.Execution.Mode, domain.ExecutionModeYOLO)
	}
}

func TestLoadAllowsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	content := strings.NewReplacer(
		`proxy_url: "socks5://127.0.0.1:10808"`, `proxy_url: ""`,
		`cookie: " existing-cookie "`, `cookie: ""`,
		`save_path: " /downloads "`, `save_path: ""`,
	).Replace(validYAML)
	configuration, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.Tracker.ProxyURL != "" || configuration.Tracker.Cookie != "" || configuration.Client.SavePath != "" {
		t.Errorf("optional fields were not empty")
	}
}

func TestLoadRejectsMissingPath(t *testing.T) {
	t.Parallel()

	if _, err := Load(" "); !errors.Is(err, ErrPathRequired) {
		t.Fatalf("Load() error = %v, want ErrPathRequired", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, ErrReadConfig) {
		t.Fatalf("Load() error = %v, want ErrReadConfig", err)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(strings.Repeat(" ", maxConfigSize+1))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write oversized fixture: %v", err)
	}
	if _, err := Load(path); !errors.Is(err, ErrConfigTooLarge) {
		t.Fatalf("Load() error = %v, want ErrConfigTooLarge", err)
	}
}

func TestDiskLimitBoundary(t *testing.T) {
	t.Parallel()

	maxGiB := int64(math.MaxInt64) / gib
	content := strings.Replace(validYAML, "max_disk_usage_gib: 500", "max_disk_usage_gib: "+strconv.FormatInt(maxGiB, 10), 1)
	configuration, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.Strategy.MaxDiskUsageBytes != maxGiB*gib {
		t.Errorf("MaxDiskUsageBytes = %d", configuration.Strategy.MaxDiskUsageBytes)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}
