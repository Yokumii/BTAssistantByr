package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/config"
	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/service/topfree"
)

const applicationFixtureHash = "0123456789abcdef0123456789abcdef01234567"

func TestApplicationRequestApprovalDeclinesBeforeMutation(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()

	var output bytes.Buffer
	application, err := Assemble(
		context.Background(),
		fixture.configuration(domain.ExecutionModeRequestApproval, true),
		strings.NewReader("cancel\n"),
		&output,
	)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	err = application.Run(context.Background())
	if !errors.Is(err, topfree.ErrExecutionDeclined) {
		t.Fatalf("Run() error = %v, want ErrExecutionDeclined", err)
	}
	if got := fixture.mutations.Load(); got != 0 {
		t.Errorf("qBittorrent mutations = %d, want 0", got)
	}
	for _, expected := range []string{"添加 sc 标签：1", "删除种子及文件：0", "下载种子：0", "输入 APPLY"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("approval output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestApplicationYOLOExecutesAndStopsOnCancellation(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.onMutation = cancel

	application, err := Assemble(ctx, fixture.configuration(domain.ExecutionModeYOLO, true), nil, nil)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := fixture.mutations.Load(); got != 1 {
		t.Errorf("qBittorrent mutations = %d, want 1", got)
	}
}

func TestAssembleLogsIntoBYRWhenCookieIsEmpty(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	application, err := AssembleWithLogger(
		context.Background(),
		fixture.configuration(domain.ExecutionModeYOLO, false),
		nil,
		nil,
		logger,
	)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if application == nil {
		t.Fatal("Assemble() returned a nil application")
	}
	if got := fixture.byrLogins.Load(); got != 1 {
		t.Errorf("BYR login requests = %d, want 1", got)
	}
	for _, expected := range []string{"assembling application", "BYR session ready", "qBittorrent session ready", "application assembly completed"} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("assembly logs do not contain %q:\n%s", expected, logs.String())
		}
	}
	for _, secret := range []string{"byr-password", "qbit-password", "tracker-session", "qbit-session"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("assembly logs contain secret %q", secret)
		}
	}
}

func TestAssembleRejectsInvalidModeBeforeNetworkAccess(t *testing.T) {
	t.Parallel()

	_, err := Assemble(context.Background(), config.Config{
		Execution: config.Execution{Mode: "invalid"},
	}, nil, nil)
	if !errors.Is(err, topfree.ErrInvalidExecutionMode) {
		t.Fatalf("Assemble() error = %v, want ErrInvalidExecutionMode", err)
	}
}

type lifecycleFixture struct {
	*httptest.Server
	byrLogins  atomic.Int32
	mutations  atomic.Int32
	onMutation func()
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	fixture := &lifecycleFixture{}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fixture.serveHTTP(t, response, request)
	}))
	return fixture
}

func (fixture *lifecycleFixture) configuration(mode domain.ExecutionMode, withBYRCookie bool) config.Config {
	trackerCookie := ""
	if withBYRCookie {
		trackerCookie = "tracker-session"
	}
	return config.Config{
		Tracker: config.Tracker{
			BaseURL:  fixture.URL + "/tracker",
			Username: "byr-user",
			Password: "byr-password",
			Cookie:   trackerCookie,
		},
		Client: config.Client{
			BaseURL:  fixture.URL + "/webui",
			Username: "qbit-user",
			Password: "qbit-password",
			SavePath: "/downloads",
		},
		Strategy: config.Strategy{
			MaxDiskUsageBytes: 10 * (1 << 30),
			MinFreeSpaceBytes: 10 * (1 << 30),
			RefreshInterval:   time.Hour,
		},
		Execution: config.Execution{Mode: mode},
	}
}

func (fixture *lifecycleFixture) serveHTTP(t *testing.T, response http.ResponseWriter, request *http.Request) {
	t.Helper()
	switch request.URL.Path {
	case "/tracker/api/v2/login.php":
		fixture.byrLogins.Add(1)
		http.SetCookie(response, &http.Cookie{Name: "auth_token", Value: "tracker-session", Path: "/"})
		http.SetCookie(response, &http.Cookie{Name: "session_id", Value: "tracker-session-id", Path: "/"})
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{"success":true,"code":200,"data":{"auth_token":"api-token"}}`)
	case "/webui/api/v2/auth/login":
		http.SetCookie(response, &http.Cookie{Name: "QBT_SID_8080", Value: "qbit-session", Path: "/"})
		response.WriteHeader(http.StatusNoContent)
	case "/webui/api/v2/app/webapiVersion":
		assertCookie(t, request, "QBT_SID_8080", "qbit-session")
		fmt.Fprint(response, "2.15.2")
	case "/webui/api/v2/app/getFreeSpaceAtPathAction":
		assertCookie(t, request, "QBT_SID_8080", "qbit-session")
		if request.URL.Query().Get("path") != "/downloads" {
			t.Errorf("unexpected free-space path: %q", request.URL.Query().Get("path"))
		}
		fmt.Fprint(response, 100*(1<<30))
	case "/tracker/torrents.php":
		assertCookie(t, request, "auth_token", "tracker-session")
		fmt.Fprint(response, applicationTorrentPage())
	case "/tracker/details.php":
		assertCookie(t, request, "auth_token", "tracker-session")
		fmt.Fprintf(response, "<p>Hash码 %s</p>", applicationFixtureHash)
	case "/webui/api/v2/torrents/info":
		assertCookie(t, request, "QBT_SID_8080", "qbit-session")
		fmt.Fprintf(response, `[{"hash":%q,"name":"Existing TopFree","size":1073741824,"completed":1073741824,"amount_left":0,"state":"stalledUP","added_on":1720000000,"save_path":"/downloads","tags":""}]`, applicationFixtureHash)
	case "/webui/api/v2/torrents/addTags":
		assertCookie(t, request, "QBT_SID_8080", "qbit-session")
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse addTags form: %v", err)
		}
		if request.PostForm.Get("hashes") != applicationFixtureHash || request.PostForm.Get("tags") != topfree.ManagedTag {
			t.Errorf("unexpected addTags form: %v", request.PostForm)
		}
		fixture.mutations.Add(1)
		if fixture.onMutation != nil {
			fixture.onMutation()
		}
		response.WriteHeader(http.StatusNoContent)
	default:
		http.Error(response, "unexpected endpoint", http.StatusNotFound)
	}
}

func applicationTorrentPage() string {
	return `<html><body><table class="torrents">
<tr>
  <td>category</td>
  <td><div class="sticky-one" title="置顶至2026-08-13 10:00:00"></div><a href="details.php?id=1" title="Existing TopFree">Existing TopFree</a><img class="pro_free"></td>
  <td>comments</td><td>time</td><td>uploader</td><td>1 GiB</td><td>2</td><td>1</td>
</tr>
</table></body></html>`
}

func assertCookie(t *testing.T, request *http.Request, name, value string) {
	t.Helper()
	cookie, err := request.Cookie(name)
	if err != nil || cookie.Value != value {
		t.Errorf("cookie %s is missing or invalid", name)
	}
}
