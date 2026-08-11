package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const (
	fixtureHashOne = "0123456789abcdef0123456789abcdef01234567"
	fixtureHashTwo = "89abcdef0123456789abcdef0123456789abcdef"
)

func TestClientListByTag(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/webui/api/v2/torrents/info" {
			http.Error(response, "unexpected endpoint", http.StatusNotFound)
			return
		}
		if request.URL.Query().Get("tag") != "sc" {
			http.Error(response, "unexpected tag", http.StatusBadRequest)
			return
		}
		assertAuthenticatedRequest(t, request)
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(response, `[{"hash":%q,"name":"Fixture","size":2048,"added_on":1720000000,"save_path":"/downloads","tags":"sc, toDel"}]`, fixtureHashOne)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	torrents, err := client.ListByTag(context.Background(), "sc")
	if err != nil {
		t.Fatalf("ListByTag() error = %v", err)
	}
	if len(torrents) != 1 {
		t.Fatalf("len(torrents) = %d, want 1", len(torrents))
	}
	torrent := torrents[0]
	if torrent.Hash != fixtureHashOne || torrent.Name != "Fixture" || torrent.SizeBytes != 2048 || torrent.SavePath != "/downloads" {
		t.Errorf("unexpected torrent: %+v", torrent)
	}
	if !torrent.AddedAt.Equal(time.Unix(1720000000, 0)) {
		t.Errorf("AddedAt = %s", torrent.AddedAt)
	}
	if len(torrent.Tags) != 2 || torrent.Tags[0] != "sc" || torrent.Tags[1] != "toDel" {
		t.Errorf("Tags = %#v", torrent.Tags)
	}
}

func TestClientListAll(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/webui/api/v2/torrents/info" {
			http.Error(response, "unexpected endpoint", http.StatusNotFound)
			return
		}
		if request.URL.RawQuery != "" {
			http.Error(response, "unexpected query", http.StatusBadRequest)
			return
		}
		assertAuthenticatedRequest(t, request)
		fmt.Fprint(response, "[]")
	}))
	defer server.Close()

	client := newTestClient(t, server)
	torrents, err := client.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(torrents) != 0 {
		t.Fatalf("len(torrents) = %d, want 0", len(torrents))
	}
}

func TestClientMutationRequests(t *testing.T) {
	t.Parallel()

	var mutex sync.Mutex
	called := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assertAuthenticatedRequest(t, request)
		if request.Method != http.MethodPost {
			http.Error(response, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		mutex.Lock()
		called[request.URL.Path]++
		mutex.Unlock()

		switch request.URL.Path {
		case "/webui/api/v2/torrents/add":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm() error = %v", err)
				return
			}
			if request.FormValue("urls") != "https://byr.pt/download.php?id=1&passkey=fixture" || request.FormValue("savepath") != "/downloads" || request.FormValue("tags") != "sc,toDownload" {
				t.Errorf("unexpected add form: %#v", request.MultipartForm.Value)
			}
			response.WriteHeader(http.StatusAccepted)
			return
		case "/webui/api/v2/torrents/addTags":
			assertForm(t, request, fixtureHashOne+"|"+fixtureHashTwo, "sc,toDel", "")
		case "/webui/api/v2/torrents/removeTags":
			assertForm(t, request, fixtureHashOne, "toDel", "")
		case "/webui/api/v2/torrents/reannounce":
			assertForm(t, request, fixtureHashOne, "", "")
		case "/webui/api/v2/torrents/stop":
			assertForm(t, request, fixtureHashOne, "", "")
		case "/webui/api/v2/torrents/delete":
			assertForm(t, request, fixtureHashOne, "", "true")
		default:
			http.Error(response, "unexpected endpoint", http.StatusNotFound)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	if err := client.Add(ctx, port.AddTorrentRequest{
		SourceURL: "https://byr.pt/download.php?id=1&passkey=fixture",
		SavePath:  "/downloads",
		Tags:      []string{"sc", "toDownload"},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := client.AddTags(ctx, []domain.TorrentHash{fixtureHashOne, fixtureHashTwo}, []string{"sc", "toDel"}); err != nil {
		t.Fatalf("AddTags() error = %v", err)
	}
	if err := client.RemoveTags(ctx, []domain.TorrentHash{fixtureHashOne}, []string{"toDel"}); err != nil {
		t.Fatalf("RemoveTags() error = %v", err)
	}
	if err := client.Reannounce(ctx, []domain.TorrentHash{fixtureHashOne}); err != nil {
		t.Fatalf("Reannounce() error = %v", err)
	}
	if err := client.Pause(ctx, []domain.TorrentHash{fixtureHashOne}); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if err := client.Delete(ctx, []domain.TorrentHash{fixtureHashOne}, true); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	for _, path := range []string{
		"/webui/api/v2/torrents/add",
		"/webui/api/v2/torrents/addTags",
		"/webui/api/v2/torrents/removeTags",
		"/webui/api/v2/torrents/reannounce",
		"/webui/api/v2/torrents/stop",
		"/webui/api/v2/torrents/delete",
	} {
		if called[path] != 1 {
			t.Errorf("calls to %s = %d, want 1", path, called[path])
		}
	}
}

func TestClientValidatesMutationsBeforeRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := newTestClient(t, server)

	tests := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{name: "empty list tag", run: func() error { _, err := client.ListByTag(context.Background(), " "); return err }, wantErr: ErrTagRequired},
		{name: "missing hashes", run: func() error { return client.Reannounce(context.Background(), nil) }, wantErr: ErrHashesRequired},
		{name: "unsafe hash", run: func() error { return client.Pause(context.Background(), []domain.TorrentHash{"all"}) }, wantErr: ErrInvalidTorrentHash},
		{name: "missing tags", run: func() error { return client.AddTags(context.Background(), []domain.TorrentHash{fixtureHashOne}, nil) }, wantErr: ErrTagRequired},
		{name: "comma in tag", run: func() error {
			return client.AddTags(context.Background(), []domain.TorrentHash{fixtureHashOne}, []string{"sc,toDel"})
		}, wantErr: ErrTagRequired},
		{name: "invalid source", run: func() error {
			return client.Add(context.Background(), port.AddTorrentRequest{SourceURL: "file:///tmp/test.torrent"})
		}, wantErr: ErrInvalidSourceURL},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.wantErr) {
				t.Errorf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want 0", requests.Load())
	}
}

func TestClientReportsExpiredSession(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.ListByTag(context.Background(), "sc")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}

func TestClientRejectsInvalidListResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(response, `[{"hash":"invalid"}]`)
	}))
	defer server.Close()
	client := newTestClient(t, server)

	_, err := client.ListByTag(context.Background(), "sc")
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error = %v, want ErrInvalidResponse", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	authenticator, err := NewAuthenticator(server.URL+"/webui", server.Client())
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	client, err := authenticator.NewClient(Credential{CookieName: "QBT_SID_8080", SID: "fixture-session"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func assertAuthenticatedRequest(t *testing.T, request *http.Request) {
	t.Helper()
	cookie, err := request.Cookie("QBT_SID_8080")
	if err != nil || cookie.Value != "fixture-session" {
		t.Errorf("unexpected session cookie")
	}
	if referer := request.Header.Get("Referer"); referer != "http://"+request.Host {
		t.Errorf("Referer = %q", referer)
	}
}

func assertForm(t *testing.T, request *http.Request, hashes, tags, deleteFiles string) {
	t.Helper()
	if err := request.ParseForm(); err != nil {
		t.Errorf("ParseForm() error = %v", err)
		return
	}
	if request.PostForm.Get("hashes") != hashes {
		t.Errorf("hashes = %q", request.PostForm.Get("hashes"))
	}
	if request.PostForm.Get("tags") != tags {
		t.Errorf("tags = %q", request.PostForm.Get("tags"))
	}
	if request.PostForm.Get("deleteFiles") != deleteFiles {
		t.Errorf("deleteFiles = %q", request.PostForm.Get("deleteFiles"))
	}
}

func TestValidSourceURL(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"https://byr.pt/download.php?id=1&passkey=fixture",
		"magnet:?xt=urn:btih:" + fixtureHashOne,
		"bc://bt/fixture",
	} {
		if !validSourceURL(source) {
			t.Errorf("validSourceURL(%q) = false", source)
		}
	}
	for _, source := range []string{"", "file:///tmp/test.torrent", "https:///missing-host", "magnet:"} {
		if validSourceURL(source) {
			t.Errorf("validSourceURL(%q) = true", source)
		}
	}
}

func TestSplitTags(t *testing.T) {
	t.Parallel()
	if got := strings.Join(splitTags("sc, toDel"), "|"); got != "sc|toDel" {
		t.Errorf("splitTags() = %q", got)
	}
}
