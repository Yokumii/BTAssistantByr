package byr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestClientListTopFreeAuthenticatesAndCachesHash(t *testing.T) {
	t.Parallel()

	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if cookie, err := request.Cookie(authTokenCookie); err != nil || cookie.Value != "auth-value" {
			http.Error(response, "missing auth cookie", http.StatusUnauthorized)
			return
		}
		if cookie, err := request.Cookie(sessionIDCookie); err != nil || cookie.Value != "session-value" {
			http.Error(response, "missing session cookie", http.StatusUnauthorized)
			return
		}

		switch request.URL.Path {
		case "/tracker/torrents.php":
			fmt.Fprint(response, torrentPageFixture("1.50 GiB", "12", "3"))
		case "/tracker/details.php":
			if request.URL.Query().Get("id") != "362950" {
				http.Error(response, "wrong id", http.StatusBadRequest)
				return
			}
			detailRequests.Add(1)
			fmt.Fprint(response, "<p>Hash码 0123456789abcdef0123456789abcdef01234567</p>")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL+"/tracker", server.Client())
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	client, err := authenticator.NewClient(Credential{AuthToken: "auth-value", SessionID: "session-value"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	for call := 0; call < 2; call++ {
		candidates, err := client.ListTopFree(context.Background())
		if err != nil {
			t.Fatalf("ListTopFree() call %d error = %v", call+1, err)
		}
		if len(candidates) != 1 || candidates[0].Hash != "0123456789abcdef0123456789abcdef01234567" {
			t.Fatalf("ListTopFree() call %d candidates = %+v", call+1, candidates)
		}
	}
	if got := detailRequests.Load(); got != 1 {
		t.Errorf("details requests = %d, want 1", got)
	}
}

func TestClientReportsExpiredSession(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", "/login.php")
		response.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	client, err := authenticator.NewClient(Credential{AuthToken: "auth-value"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.ListTopFree(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}

func TestNewClientRequiresAuthToken(t *testing.T) {
	t.Parallel()

	authenticator, err := NewAuthenticator("https://byr.pt", nil)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	_, err = authenticator.NewClient(Credential{})
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("error = %v, want ErrAuthenticationFailed", err)
	}
}

func TestClientDownloadURLCachesPasskey(t *testing.T) {
	t.Parallel()

	const passkey = "0123456789abcdef0123456789abcdef"
	var passkeyRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tracker/usercp.php" {
			http.NotFound(response, request)
			return
		}
		if _, err := request.Cookie(authTokenCookie); err != nil {
			http.Error(response, "missing auth cookie", http.StatusUnauthorized)
			return
		}
		passkeyRequests.Add(1)
		fmt.Fprintf(response, "<table><tr><td>passkey</td><td>%s</td></tr></table>", passkey)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL+"/tracker", server.Client())
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	client, err := authenticator.NewClient(Credential{AuthToken: "auth-value"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	for _, torrentID := range []string{"362950", "362951"} {
		downloadURL, err := client.DownloadURL(context.Background(), domain.TorrentID(torrentID))
		if err != nil {
			t.Fatalf("DownloadURL(%s) error = %v", torrentID, err)
		}
		parsed, err := url.Parse(downloadURL)
		if err != nil {
			t.Fatalf("parse download URL: %v", err)
		}
		if parsed.Path != "/tracker/download.php" || parsed.Query().Get("id") != torrentID || parsed.Query().Get("passkey") != passkey {
			t.Errorf("unexpected download URL components for torrent %s", torrentID)
		}
	}
	if got := passkeyRequests.Load(); got != 1 {
		t.Errorf("passkey requests = %d, want 1", got)
	}
}

func TestClientDownloadURLRejectsInvalidTorrentID(t *testing.T) {
	t.Parallel()

	authenticator, err := NewAuthenticator("https://byr.pt", nil)
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	client, err := authenticator.NewClient(Credential{AuthToken: "auth-value"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.DownloadURL(context.Background(), "all")
	if !errors.Is(err, ErrInvalidTorrentID) {
		t.Fatalf("error = %v, want ErrInvalidTorrentID", err)
	}
}
