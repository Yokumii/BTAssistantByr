package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	sessionCookie  = "SID"
	defaultTimeout = 15 * time.Second
)

var (
	// ErrInvalidBaseURL indicates that the configured Web API base URL is unusable.
	ErrInvalidBaseURL = errors.New("invalid qBittorrent base URL")
	// ErrUsernameRequired indicates that no Web API username was supplied.
	ErrUsernameRequired = errors.New("qBittorrent username is required")
	// ErrPasswordRequired indicates that no Web API password was supplied.
	ErrPasswordRequired = errors.New("qBittorrent password is required")
	// ErrAuthenticationFailed indicates that qBittorrent did not issue a SID.
	ErrAuthenticationFailed = errors.New("qBittorrent authentication failed")
	// ErrUnexpectedStatus indicates that qBittorrent returned a non-success status.
	ErrUnexpectedStatus = errors.New("unexpected qBittorrent response status")
)

// Credential is the session credential issued by qBittorrent after login.
type Credential struct {
	SID string
}

// NewCredential validates an existing qBittorrent SID.
func NewCredential(sid string) (Credential, error) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return Credential{}, ErrAuthenticationFailed
	}

	return Credential{SID: sid}, nil
}

// Authenticator exchanges Web API credentials for a qBittorrent session.
type Authenticator struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// NewAuthenticator creates a qBittorrent authenticator.
func NewAuthenticator(baseURL string, httpClient *http.Client) (*Authenticator, error) {
	parsedURL, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Authenticator{
		baseURL:    parsedURL,
		httpClient: httpClient,
	}, nil
}

// Login obtains a SID cookie from qBittorrent Web API v2.
func (a *Authenticator) Login(ctx context.Context, username, password string) (Credential, error) {
	if strings.TrimSpace(username) == "" {
		return Credential{}, ErrUsernameRequired
	}
	if password == "" {
		return Credential{}, ErrPasswordRequired
	}

	form := url.Values{
		"username": {username},
		"password": {password},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint("api/v2/auth/login"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return Credential{}, fmt.Errorf("create qBittorrent login request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return Credential{}, fmt.Errorf("send qBittorrent login request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Credential{}, fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}

	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookie {
			return NewCredential(cookie.Value)
		}
	}

	return Credential{}, ErrAuthenticationFailed
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
