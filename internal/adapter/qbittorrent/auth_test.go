package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatorLogin(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/webui/api/v2/auth/login" {
			t.Errorf("unexpected request path: %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Method != http.MethodPost {
			t.Errorf("unexpected request method: %s", request.Method)
		}
		if referer := request.Header.Get("Referer"); referer != "http://"+request.Host {
			t.Errorf("unexpected Referer: %q", referer)
		}
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse login form: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if username := request.PostForm.Get("username"); username != "admin" {
			t.Errorf("unexpected username: %q", username)
		}
		if password := request.PostForm.Get("password"); password != "secret" {
			t.Errorf("unexpected password: %q", password)
		}

		http.SetCookie(response, &http.Cookie{Name: legacySessionCookie, Value: "qbt-session", Path: "/"})
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL+"/webui/", server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	credential, err := authenticator.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if credential.SID != "qbt-session" {
		t.Fatalf("unexpected SID: %q", credential.SID)
	}
	if credential.CookieName != legacySessionCookie {
		t.Fatalf("unexpected cookie name: %q", credential.CookieName)
	}
}

func TestAuthenticatorLoginWithVersionedSessionCookie(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if referer := request.Header.Get("Referer"); referer != "http://"+request.Host {
			t.Errorf("unexpected Referer: %q", referer)
		}
		http.SetCookie(response, &http.Cookie{Name: "QBT_SID_8080", Value: "qbt-v5-session", Path: "/"})
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	credential, err := authenticator.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if credential.SID != "qbt-v5-session" {
		t.Fatalf("unexpected SID: %q", credential.SID)
	}
	if credential.CookieName != "QBT_SID_8080" {
		t.Fatalf("unexpected cookie name: %q", credential.CookieName)
	}
}

func TestAuthenticatorLoginRejectsMissingInput(t *testing.T) {
	t.Parallel()

	authenticator, err := NewAuthenticator("http://127.0.0.1:8080", nil)
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	tests := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{name: "username", password: "secret", wantErr: ErrUsernameRequired},
		{name: "password", username: "admin", wantErr: ErrPasswordRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := authenticator.Login(context.Background(), test.username, test.password)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("unexpected error: got %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAuthenticatorLoginRejectsResponseWithoutSID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "admin", "wrong-password")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
	}
}

func TestAuthenticatorLoginRejectsUnexpectedStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "admin", "secret")
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrUnexpectedStatus)
	}
}

func TestNewAuthenticatorRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{"", "127.0.0.1:8080", "ftp://localhost", "http://user@localhost", "http://localhost#fragment"} {
		baseURL := baseURL
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()

			_, err := NewAuthenticator(baseURL, nil)
			if !errors.Is(err, ErrInvalidBaseURL) {
				t.Fatalf("unexpected error for %q: got %v, want %v", baseURL, err, ErrInvalidBaseURL)
			}
		})
	}
}

func TestNewCredential(t *testing.T) {
	t.Parallel()

	credential, err := NewCredential(" existing-session ")
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if credential.SID != "existing-session" {
		t.Fatalf("unexpected SID: %q", credential.SID)
	}
	if credential.CookieName != legacySessionCookie {
		t.Fatalf("unexpected cookie name: %q", credential.CookieName)
	}

	_, err = NewCredential(" ")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
	}
}

func TestNewCredentialWithCookie(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cookieName string
		sid        string
		wantErr    bool
	}{
		{name: "legacy", cookieName: "SID", sid: "legacy-session"},
		{name: "versioned", cookieName: "QBT_SID_8080", sid: "versioned-session"},
		{name: "minimum port", cookieName: "QBT_SID_1", sid: "versioned-session"},
		{name: "maximum port", cookieName: "QBT_SID_65535", sid: "versioned-session"},
		{name: "unknown cookie", cookieName: "OTHER", sid: "session", wantErr: true},
		{name: "missing port", cookieName: "QBT_SID_", sid: "session", wantErr: true},
		{name: "non-numeric port", cookieName: "QBT_SID_http", sid: "session", wantErr: true},
		{name: "invalid port", cookieName: "QBT_SID_65536", sid: "session", wantErr: true},
		{name: "missing SID", cookieName: "QBT_SID_8080", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			credential, err := NewCredentialWithCookie(test.cookieName, test.sid)
			if test.wantErr {
				if !errors.Is(err, ErrAuthenticationFailed) {
					t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
				}
				return
			}
			if err != nil {
				t.Fatalf("create credential: %v", err)
			}
			if credential.CookieName != test.cookieName || credential.SID != test.sid {
				t.Fatalf("unexpected credential: %+v", credential)
			}
		})
	}
}
