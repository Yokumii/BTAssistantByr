package byr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authTokenCookie = "auth_token"
	sessionIDCookie = "session_id"
	defaultTimeout  = 15 * time.Second
	maxResponseSize = 1 << 20
)

var (
	// ErrInvalidBaseURL indicates that the configured BYR base URL is unusable.
	ErrInvalidBaseURL = errors.New("invalid BYR base URL")
	// ErrInvalidProxyURL indicates that the configured proxy URL is unusable.
	ErrInvalidProxyURL = errors.New("invalid BYR proxy URL")
	// ErrUnsupportedTransport indicates that a custom transport cannot be cloned safely.
	ErrUnsupportedTransport = errors.New("unsupported HTTP transport for BYR proxy")
	// ErrUsernameRequired indicates that no BYR username was supplied.
	ErrUsernameRequired = errors.New("BYR username is required")
	// ErrPasswordRequired indicates that no BYR password was supplied.
	ErrPasswordRequired = errors.New("BYR password is required")
	// ErrAuthenticationFailed indicates that BYR did not issue an auth token.
	ErrAuthenticationFailed = errors.New("BYR authentication failed")
	// ErrUnexpectedStatus indicates that BYR returned an unsupported HTTP status.
	ErrUnexpectedStatus = errors.New("unexpected BYR response status")
	// ErrInvalidResponse indicates that BYR returned a malformed login response.
	ErrInvalidResponse = errors.New("invalid BYR login response")
)

// Credential is the session credential issued by BYR after login.
type Credential struct {
	AccessToken string
	AuthToken   string
	SessionID   string
}

// NewCredential validates an existing BYR auth token.
func NewCredential(authToken string) (Credential, error) {
	authToken = strings.TrimSpace(authToken)
	if authToken == "" {
		return Credential{}, ErrAuthenticationFailed
	}

	return Credential{AuthToken: authToken}, nil
}

// Authenticator exchanges a BYR username and password for a session credential.
type Authenticator struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewAuthenticator creates a BYR authenticator.
func NewAuthenticator(baseURL string, httpClient *http.Client) (*Authenticator, error) {
	return NewAuthenticatorWithProxy(baseURL, "", httpClient)
}

// NewAuthenticatorWithProxy creates a BYR authenticator using proxyURL when it is not empty.
func NewAuthenticatorWithProxy(baseURL, proxyURL string, httpClient *http.Client) (*Authenticator, error) {
	parsedURL, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	client, err := cloneHTTPClient(httpClient, proxyURL)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Authenticator{
		baseURL:    parsedURL,
		httpClient: client,
	}, nil
}

// Login obtains BYR API and cookie credentials.
func (a *Authenticator) Login(ctx context.Context, username, password string) (Credential, error) {
	if strings.TrimSpace(username) == "" {
		return Credential{}, ErrUsernameRequired
	}
	if password == "" {
		return Credential{}, ErrPasswordRequired
	}

	payload := struct {
		Type     string `json:"type"`
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}{
		Type:     "username",
		Username: username,
		Password: password,
		Remember: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Credential{}, fmt.Errorf("encode BYR login request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint("api/v2/login.php"),
		bytes.NewReader(body),
	)
	if err != nil {
		return Credential{}, fmt.Errorf("create BYR login request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return Credential{}, fmt.Errorf("send BYR login request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
		return Credential{}, fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}

	var result struct {
		Success bool `json:"success"`
		Code    int  `json:"code"`
		Data    struct {
			AuthToken string `json:"auth_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize)).Decode(&result); err != nil {
		return Credential{}, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if !result.Success || result.Code != http.StatusOK || strings.TrimSpace(result.Data.AuthToken) == "" {
		return Credential{}, ErrAuthenticationFailed
	}

	credential := Credential{AccessToken: strings.TrimSpace(result.Data.AuthToken)}
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case authTokenCookie:
			credential.AuthToken = strings.TrimSpace(cookie.Value)
		case sessionIDCookie:
			credential.SessionID = strings.TrimSpace(cookie.Value)
		}
	}
	if credential.AuthToken == "" || credential.SessionID == "" {
		return Credential{}, ErrAuthenticationFailed
	}

	return credential, nil
}

func (a *Authenticator) endpoint(relativePath string) string {
	endpoint := *a.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + relativePath
	endpoint.RawPath = ""
	return endpoint.String()
}

func parseBaseURL(rawURL string) (*url.URL, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Host == "" {
		return nil, ErrInvalidBaseURL
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, ErrInvalidBaseURL
	}
	if parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, ErrInvalidBaseURL
	}

	return parsedURL, nil
}

func cloneHTTPClient(httpClient *http.Client, rawProxyURL string) (*http.Client, error) {
	client := *httpClient
	proxyURL, err := parseProxyURL(rawProxyURL)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil {
		return &client, nil
	}

	var transport *http.Transport
	switch original := httpClient.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = original.Clone()
	default:
		return nil, ErrUnsupportedTransport
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	client.Transport = transport

	return &client, nil
}

func parseProxyURL(rawProxyURL string) (*url.URL, error) {
	rawProxyURL = strings.TrimSpace(rawProxyURL)
	if rawProxyURL == "" {
		return nil, nil
	}

	proxyURL, err := url.Parse(rawProxyURL)
	if err != nil || proxyURL.Host == "" {
		return nil, ErrInvalidProxyURL
	}
	switch proxyURL.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, ErrInvalidProxyURL
	}
	if (proxyURL.Path != "" && proxyURL.Path != "/") || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return nil, ErrInvalidProxyURL
	}

	return proxyURL, nil
}
