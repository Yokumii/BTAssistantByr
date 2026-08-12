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

	"github.com/Yokumii/BTAssistantByr/internal/adapter/byr"
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

func TestAssembleReusesCachedBYRSessionBeforeLoggingInAgain(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()
	configuration := fixture.configuration(domain.ExecutionModeYOLO, false)

	for assembly := 1; assembly <= 2; assembly++ {
		application, err := Assemble(context.Background(), configuration, nil, nil)
		if err != nil {
			t.Fatalf("Assemble() call %d error = %v", assembly, err)
		}
		if application == nil {
			t.Fatalf("Assemble() call %d returned nil", assembly)
		}
	}
	if got := fixture.byrLogins.Load(); got != 1 {
		t.Fatalf("BYR login requests = %d, want 1 across two starts", got)
	}
}

func TestLoadBYRCredentialPrefersCacheOverConfiguredCookie(t *testing.T) {
	t.Parallel()

	cache, err := byr.NewSessionCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	configuration := config.Tracker{BaseURL: "https://byr.pt", Username: "alice", Cookie: "configured-cookie"}
	if err := cache.Save(configuration.BaseURL, configuration.Username, byr.Credential{AuthToken: "cached-token", SessionID: "cached-session"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	credential, source, err := loadBYRCredential(cache, configuration)
	if err != nil {
		t.Fatalf("loadBYRCredential() error = %v", err)
	}
	if source != byrCredentialSourceCache || credential.AuthToken != "cached-token" || credential.SessionID != "cached-session" {
		t.Fatalf("credential=%+v source=%q", credential, source)
	}
}

func TestLoadBYRCredentialFallsBackInPriorityOrder(t *testing.T) {
	t.Parallel()

	cache, err := byr.NewSessionCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	configuration := config.Tracker{BaseURL: "https://byr.pt", Username: "alice", Cookie: "configured-cookie"}
	credential, source, err := loadBYRCredential(cache, configuration)
	if err != nil || source != byrCredentialSourceConfig || credential.AuthToken != "configured-cookie" {
		t.Fatalf("configured cookie fallback credential=%+v source=%q error=%v", credential, source, err)
	}
	configuration.Cookie = ""
	credential, source, err = loadBYRCredential(cache, configuration)
	if err != nil || source != byrCredentialSourceLogin || credential != (byr.Credential{}) {
		t.Fatalf("login fallback credential=%+v source=%q error=%v", credential, source, err)
	}
}

func TestApplicationRenewsExpiredConfiguredBYRCookie(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()
	fixture.expireInitialBYRSession = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.onMutation = cancel
	fixture.renewalFailures = 1
	configuration := fixture.configuration(domain.ExecutionModeYOLO, true)
	configuration.Runtime.NetworkRetryCount = 1

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	application, err := AssembleWithLogger(
		ctx,
		configuration,
		nil,
		nil,
		logger,
	)
	if err != nil {
		t.Fatalf("AssembleWithLogger() error = %v", err)
	}
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := fixture.byrLogins.Load(); got != 2 {
		t.Fatalf("BYR renewal logins = %d, want 2", got)
	}
	if got := fixture.mutations.Load(); got != 1 {
		t.Fatalf("qBittorrent mutations = %d, want 1", got)
	}
	cache, err := byr.NewSessionCache(configuration.Runtime.DataDir)
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	credential, found, err := cache.Load(configuration.Tracker.BaseURL, configuration.Tracker.Username)
	if err != nil {
		t.Fatalf("Load() renewed cache error = %v", err)
	}
	if !found || credential.AuthToken != "tracker-session-renewed" || credential.SessionID != "tracker-session-id-renewed" {
		t.Fatalf("renewed cache credential=%+v found=%t", credential, found)
	}
	for _, expected := range []string{"BYR session expired; reauthenticating", "network request failed; retrying", "BYR session renewed"} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("logs do not contain %q:\n%s", expected, logs.String())
		}
	}
	for _, secret := range []string{"byr-password", "tracker-session", "tracker-session-renewed"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("session renewal logs contain secret %q", secret)
		}
	}
}

func TestApplicationFallsBackFromExpiredCacheAndConfiguredCookie(t *testing.T) {
	t.Parallel()

	fixture := newLifecycleFixture(t)
	defer fixture.Close()
	fixture.expireInitialBYRSession = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.onMutation = cancel
	configuration := fixture.configuration(domain.ExecutionModeYOLO, true)
	cache, err := byr.NewSessionCache(configuration.Runtime.DataDir)
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	if err := cache.Save(
		configuration.Tracker.BaseURL,
		configuration.Tracker.Username,
		byr.Credential{AuthToken: "expired-cached-token", SessionID: "expired-cached-session"},
	); err != nil {
		t.Fatalf("Save() expired cache error = %v", err)
	}

	application, err := Assemble(ctx, configuration, nil, nil)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := fixture.byrLogins.Load(); got != 1 {
		t.Fatalf("BYR login requests = %d, want 1 after cache and configured cookie expired", got)
	}
	credential, found, err := cache.Load(configuration.Tracker.BaseURL, configuration.Tracker.Username)
	if err != nil || !found || credential.AuthToken != "tracker-session-renewed" {
		t.Fatalf("renewed cache credential=%+v found=%t error=%v", credential, found, err)
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
	byrLogins               atomic.Int32
	mutations               atomic.Int32
	onMutation              func()
	expireInitialBYRSession bool
	renewalFailures         int32
	dataDir                 string
}

func newLifecycleFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	fixture := &lifecycleFixture{dataDir: t.TempDir()}
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
			MaxDiskUsageBytes:  10 * (1 << 30),
			MinFreeSpaceBytes:  10 * (1 << 30),
			RefreshInterval:    time.Hour,
			QueueCheckInterval: time.Hour,
		},
		Execution: config.Execution{Mode: mode},
		Runtime: config.Runtime{
			DataDir:           fixture.dataDir,
			NetworkRetryCount: 0,
		},
	}
}

func (fixture *lifecycleFixture) serveHTTP(t *testing.T, response http.ResponseWriter, request *http.Request) {
	t.Helper()
	switch request.URL.Path {
	case "/tracker/api/v2/login.php":
		loginAttempt := fixture.byrLogins.Add(1)
		if fixture.expireInitialBYRSession && loginAttempt <= fixture.renewalFailures {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		authToken := "tracker-session"
		sessionID := "tracker-session-id"
		if fixture.expireInitialBYRSession {
			authToken = "tracker-session-renewed"
			sessionID = "tracker-session-id-renewed"
		}
		http.SetCookie(response, &http.Cookie{Name: "auth_token", Value: authToken, Path: "/"})
		http.SetCookie(response, &http.Cookie{Name: "session_id", Value: sessionID, Path: "/"})
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
		if fixture.expireInitialBYRSession {
			cookie, cookieErr := request.Cookie("auth_token")
			if cookieErr != nil || cookie.Value != "tracker-session-renewed" {
				response.Header().Set("Location", "/tracker/login.php")
				response.WriteHeader(http.StatusFound)
				return
			}
			assertCookie(t, request, "auth_token", "tracker-session-renewed")
		} else {
			assertCookie(t, request, "auth_token", "tracker-session")
		}
		fmt.Fprint(response, applicationTorrentPage())
	case "/tracker/details.php":
		expectedToken := "tracker-session"
		if fixture.expireInitialBYRSession {
			expectedToken = "tracker-session-renewed"
		}
		assertCookie(t, request, "auth_token", expectedToken)
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
