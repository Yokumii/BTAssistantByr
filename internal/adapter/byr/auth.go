package byr

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
	authTokenCookie = "auth_token"
	defaultTimeout  = 15 * time.Second
)

var (
	// ErrInvalidBaseURL indicates that the configured BYR base URL is unusable.
	ErrInvalidBaseURL = errors.New("invalid BYR base URL")
	// ErrUsernameRequired indicates that no BYR username was supplied.
	ErrUsernameRequired = errors.New("BYR username is required")
	// ErrPasswordRequired indicates that no BYR password was supplied.
	ErrPasswordRequired = errors.New("BYR password is required")
	// ErrAuthenticationFailed indicates that BYR did not issue an auth token.
	ErrAuthenticationFailed = errors.New("BYR authentication failed")
	// ErrUnexpectedStatus indicates that BYR returned an unsupported HTTP status.
	ErrUnexpectedStatus = errors.New("unexpected BYR response status")
)

// Credential is the session credential issued by BYR after login.
type Credential struct {
	AuthToken string
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
	parsedURL, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	client := *httpClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Authenticator{
		baseURL:    parsedURL,
		httpClient: &client,
	}, nil
}

// Login obtains a BYR auth_token cookie without following the login redirect.
func (a *Authenticator) Login(ctx context.Context, username, password string) (Credential, error) {
	if strings.TrimSpace(username) == "" {
		return Credential{}, ErrUsernameRequired
	}
	if password == "" {
		return Credential{}, ErrPasswordRequired
	}

	form := url.Values{
		"logintype": {"username"},
		"userinput": {username},
		"password":  {password},
		"autologin": {"yes"},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.endpoint("takelogin.php"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return Credential{}, fmt.Errorf("create BYR login request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := a.httpClient.Do(request)
	if err != nil {
		return Credential{}, fmt.Errorf("send BYR login request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
		return Credential{}, fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}

	for _, cookie := range response.Cookies() {
		if cookie.Name == authTokenCookie {
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
