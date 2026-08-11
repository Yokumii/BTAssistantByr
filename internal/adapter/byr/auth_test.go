package byr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

func TestAuthenticatorLogin(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tracker/api/v2/login.php" {
			t.Errorf("unexpected request path: %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Method != http.MethodPost {
			t.Errorf("unexpected request method: %s", request.Method)
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("unexpected content type: %s", contentType)
		}

		var payload struct {
			Type     string `json:"type"`
			Username string `json:"username"`
			Password string `json:"password"`
			Remember bool   `json:"remember"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode login payload: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Type != "username" || payload.Username != "alice" || payload.Password != "secret" || payload.Remember {
			t.Errorf("unexpected login payload: type=%q username=%q remember=%t", payload.Type, payload.Username, payload.Remember)
		}

		writeLoginSuccess(t, response, "api-token", "cookie-token", "session-token")
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
	if credential.AccessToken != "api-token" {
		t.Fatalf("unexpected access token: %q", credential.AccessToken)
	}
	if credential.AuthToken != "cookie-token" {
		t.Fatalf("unexpected auth token: %q", credential.AuthToken)
	}
	if credential.SessionID != "session-token" {
		t.Fatalf("unexpected session ID: %q", credential.SessionID)
	}
}

func TestAuthenticatorLoginThroughHTTPProxy(t *testing.T) {
	t.Parallel()

	proxy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Scheme != "http" || request.URL.Host != "tracker.invalid" {
			t.Errorf("unexpected proxy target: %s", request.URL.String())
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.URL.Path != "/api/v2/login.php" {
			t.Errorf("unexpected request path: %s", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}

		writeLoginSuccess(t, response, "proxied-api-token", "proxied-cookie-token", "proxied-session")
	}))
	defer proxy.Close()

	authenticator, err := NewAuthenticatorWithProxy("http://tracker.invalid", proxy.URL, nil)
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	credential, err := authenticator.Login(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("login through proxy: %v", err)
	}
	if credential.AuthToken != "proxied-cookie-token" {
		t.Fatalf("unexpected auth token: %q", credential.AuthToken)
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
		http.SetCookie(response, &http.Cookie{Name: authTokenCookie, Value: "cookie-token", Path: "/"})
		http.SetCookie(response, &http.Cookie{Name: sessionIDCookie, Value: "session-token", Path: "/"})
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"code":200,"data":{}}`))
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

func TestAuthenticatorLoginRejectsResponseWithoutCookies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"code":200,"data":{"auth_token":"api-token"}}`))
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "alice", "secret")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrAuthenticationFailed)
	}
}

func TestAuthenticatorLoginRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`not-json`))
	}))
	defer server.Close()

	authenticator, err := NewAuthenticator(server.URL, server.Client())
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}

	_, err = authenticator.Login(context.Background(), "alice", "secret")
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrInvalidResponse)
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
	if !netretry.IsTransient(err) {
		t.Fatalf("503 error was not marked transient: %v", err)
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

func TestNewAuthenticatorRejectsInvalidProxyURL(t *testing.T) {
	t.Parallel()

	for _, proxyURL := range []string{"127.0.0.1:10808", "ftp://127.0.0.1:10808", "http://127.0.0.1:10808/path", "http://127.0.0.1:10808?x=1"} {
		proxyURL := proxyURL
		t.Run(proxyURL, func(t *testing.T) {
			t.Parallel()

			_, err := NewAuthenticatorWithProxy("https://byr.example", proxyURL, nil)
			if !errors.Is(err, ErrInvalidProxyURL) {
				t.Fatalf("unexpected error for %q: got %v, want %v", proxyURL, err, ErrInvalidProxyURL)
			}
		})
	}
}

func TestNewAuthenticatorRejectsUnsupportedProxyTransport(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not be called")
	})}

	_, err := NewAuthenticatorWithProxy("https://byr.example", "http://127.0.0.1:10808", client)
	if !errors.Is(err, ErrUnsupportedTransport) {
		t.Fatalf("unexpected error: got %v, want %v", err, ErrUnsupportedTransport)
	}
}

func TestNewAuthenticatorDoesNotMutateTransport(t *testing.T) {
	t.Parallel()

	transport := &http.Transport{}
	client := &http.Client{Transport: transport}
	authenticator, err := NewAuthenticatorWithProxy("https://byr.example", "socks5://127.0.0.1:10808", client)
	if err != nil {
		t.Fatalf("create authenticator: %v", err)
	}
	if transport.Proxy != nil {
		t.Fatal("original transport was modified")
	}
	if authenticator.httpClient.Transport == transport {
		t.Fatal("authenticator reused the original transport")
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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func writeLoginSuccess(t *testing.T, response http.ResponseWriter, accessToken, authToken, sessionID string) {
	t.Helper()

	http.SetCookie(response, &http.Cookie{Name: authTokenCookie, Value: authToken, Path: "/"})
	http.SetCookie(response, &http.Cookie{Name: sessionIDCookie, Value: sessionID, Path: "/"})
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{
		"success": true,
		"code":    http.StatusOK,
		"data": map[string]string{
			"auth_token": accessToken,
		},
	}); err != nil {
		t.Errorf("encode login response: %v", err)
	}
}
