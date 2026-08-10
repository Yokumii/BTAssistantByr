package byr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestAuthenticatorLogin(t *testing.T) {
	t.Parallel()

	var redirected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/landing" {
			redirected.Store(true)
			response.WriteHeader(http.StatusOK)
			return
		}
		if request.URL.Path != "/tracker/takelogin.php" {
			t.Errorf("unexpected request path: %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Method != http.MethodPost {
			t.Errorf("unexpected request method: %s", request.Method)
		}
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse login form: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}

		expectedFields := map[string]string{
			"logintype": "username",
			"userinput": "alice",
			"password":  "secret",
			"autologin": "yes",
		}
		for name, expected := range expectedFields {
			if actual := request.PostForm.Get(name); actual != expected {
				t.Errorf("unexpected %s: got %q, want %q", name, actual, expected)
			}
		}

		http.SetCookie(response, &http.Cookie{Name: authTokenCookie, Value: "byr-token", Path: "/"})
		http.Redirect(response, request, "/landing", http.StatusFound)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL+"/tracker/", server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	credential, err := authenticator.Login(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if credential.AuthToken != "byr-token" {
		t.Fatalf("unexpected auth token: %q", credential.AuthToken)
	}
	if redirected.Load() {
		t.Fatal("login redirect was followed")
	}
}

func TestAuthenticatorLoginRejectsMissingInput(t *testing.T) {
	t.Parallel()

	authenticator, err := NewAuthenticator("https://byr.example", nil)
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
		{name: "password", username: "alice", wantErr: ErrPasswordRequired},
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

func TestAuthenticatorLoginRejectsResponseWithoutToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "alice", "wrong-password")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
	}
}

func TestAuthenticatorLoginRejectsUnexpectedStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "alice", "secret")
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrUnexpectedStatus)
	}
}

func TestNewAuthenticatorRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{"", "byr.example", "ftp://byr.example", "https://user@byr.example", "https://byr.example?x=1"} {
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

	credential, err := NewCredential(" existing-token ")
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if credential.AuthToken != "existing-token" {
		t.Fatalf("unexpected auth token: %q", credential.AuthToken)
	}

	_, err = NewCredential(" ")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
	}
}
