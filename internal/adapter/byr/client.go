package byr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const maxPageSize = 4 << 20

var (
	// ErrSessionExpired indicates that BYR no longer accepts the supplied cookies.
	ErrSessionExpired = errors.New("BYR session expired")
	// ErrInvalidTorrentPage indicates that the torrent page cannot be parsed safely.
	ErrInvalidTorrentPage = errors.New("invalid BYR torrent page")
	// ErrTorrentHashNotFound indicates that a details page does not contain a torrent hash.
	ErrTorrentHashNotFound = errors.New("BYR torrent hash not found")
	// ErrPasskeyNotFound indicates that BYR's user page does not contain a passkey.
	ErrPasskeyNotFound = errors.New("BYR passkey not found")
	// ErrInvalidTorrentID indicates that a torrent ID cannot be used in a BYR URL.
	ErrInvalidTorrentID = errors.New("invalid BYR torrent ID")
	// ErrInvalidSessionRefreshLimit indicates that a refreshing client cannot be configured safely.
	ErrInvalidSessionRefreshLimit = errors.New("invalid BYR session refresh limit")
)

var _ port.Tracker = (*Client)(nil)

// CredentialRefresh obtains a new BYR session after the current cookies expire.
type CredentialRefresh func(context.Context) (Credential, error)

// Client accesses authenticated BYR tracker pages.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client

	credentialMu         sync.RWMutex
	credential           Credential
	credentialGeneration uint64
	refreshMu            sync.Mutex
	refreshCredential    CredentialRefresh
	maxSessionRefreshes  int

	hashMu    sync.RWMutex
	hashCache map[domain.TorrentID]domain.TorrentHash

	passkeyMu sync.RWMutex
	passkey   string
}

// DownloadURL returns an authenticated tracker URL without downloading the torrent file.
func (c *Client) DownloadURL(ctx context.Context, torrentID domain.TorrentID) (string, error) {
	if !validTorrentID(torrentID) {
		return "", ErrInvalidTorrentID
	}
	passkey, err := c.Passkey(ctx)
	if err != nil {
		return "", err
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/download.php"
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{
		"id":      {string(torrentID)},
		"passkey": {passkey},
	}.Encode()
	return endpoint.String(), nil
}

// Passkey returns the user's BYR tracker passkey and caches it for this client session.
func (c *Client) Passkey(ctx context.Context) (string, error) {
	c.passkeyMu.RLock()
	passkey := c.passkey
	c.passkeyMu.RUnlock()
	if passkey != "" {
		return passkey, nil
	}

	response, err := c.get(ctx, "usercp.php", nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	passkey, err = parsePasskey(io.LimitReader(response.Body, maxPageSize))
	if err != nil {
		return "", err
	}
	c.passkeyMu.Lock()
	c.passkey = passkey
	c.passkeyMu.Unlock()
	return passkey, nil
}

// NewClient creates an authenticated client that shares this authenticator's HTTP settings.
func (a *Authenticator) NewClient(credential Credential) (*Client, error) {
	return a.newRefreshingClient(credential, nil, 0)
}

// NewRefreshingClient creates a client that can replace expired session cookies.
func (a *Authenticator) NewRefreshingClient(credential Credential, refresh CredentialRefresh) (*Client, error) {
	return a.newRefreshingClient(credential, refresh, 1)
}

// NewRefreshingClientWithInitialFallbacks creates a client that may walk a
// longer credential fallback chain once, then returns to one renewal per read.
func (a *Authenticator) NewRefreshingClientWithInitialFallbacks(credential Credential, refresh CredentialRefresh, maxRefreshes int) (*Client, error) {
	if refresh == nil || maxRefreshes <= 0 || maxRefreshes > 2 {
		return nil, ErrInvalidSessionRefreshLimit
	}
	return a.newRefreshingClient(credential, refresh, maxRefreshes)
}

func (a *Authenticator) newRefreshingClient(credential Credential, refresh CredentialRefresh, maxRefreshes int) (*Client, error) {
	credential, err := normalizeCredential(credential)
	if err != nil {
		return nil, err
	}

	return &Client{
		baseURL:             a.baseURL,
		httpClient:          a.httpClient,
		credential:          credential,
		refreshCredential:   refresh,
		maxSessionRefreshes: maxRefreshes,
		hashCache:           make(map[domain.TorrentID]domain.TorrentHash),
	}, nil
}

func normalizeCredential(credential Credential) (Credential, error) {
	credential.AuthToken = strings.TrimSpace(credential.AuthToken)
	credential.SessionID = strings.TrimSpace(credential.SessionID)
	if credential.AuthToken == "" {
		return Credential{}, ErrAuthenticationFailed
	}
	return credential, nil
}

// ListTopFree returns all torrents on the current page that are both sticky and free.
func (c *Client) ListTopFree(ctx context.Context) ([]domain.Candidate, error) {
	response, err := c.get(ctx, "torrents.php", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	candidates, err := parseTopFree(io.LimitReader(response.Body, maxPageSize))
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		hash, err := c.TorrentHash(ctx, candidates[index].ID)
		if err != nil {
			return nil, fmt.Errorf("get hash for torrent %s: %w", candidates[index].ID, err)
		}
		candidates[index].Hash = hash
	}

	return candidates, nil
}

// TorrentHash returns a torrent hash, using an in-memory cache after the first request.
func (c *Client) TorrentHash(ctx context.Context, torrentID domain.TorrentID) (domain.TorrentHash, error) {
	if strings.TrimSpace(string(torrentID)) == "" {
		return "", ErrTorrentHashNotFound
	}

	c.hashMu.RLock()
	hash, ok := c.hashCache[torrentID]
	c.hashMu.RUnlock()
	if ok {
		return hash, nil
	}

	response, err := c.get(ctx, "details.php", url.Values{"id": {string(torrentID)}})
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	hash, err = parseTorrentHash(io.LimitReader(response.Body, maxPageSize))
	if err != nil {
		return "", err
	}
	c.hashMu.Lock()
	c.hashCache[torrentID] = hash
	c.hashMu.Unlock()

	return hash, nil
}

func (c *Client) get(ctx context.Context, relativePath string, query url.Values) (*http.Response, error) {
	maxRefreshes := c.sessionRefreshLimit()
	for refreshes := 0; ; refreshes++ {
		credential, generation := c.currentCredential()
		response, err := c.getWithCredential(ctx, relativePath, query, credential)
		if !errors.Is(err, ErrSessionExpired) {
			return response, err
		}
		if refreshes >= maxRefreshes || c.refreshCredential == nil {
			return nil, err
		}
		if err := c.refreshSession(ctx, generation); err != nil {
			return nil, fmt.Errorf("refresh BYR session: %w", err)
		}
		if maxRefreshes > 1 {
			c.consumeInitialFallbacks()
		}
	}
}

func (c *Client) getWithCredential(ctx context.Context, relativePath string, query url.Values, credential Credential) (*http.Response, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + relativePath
	endpoint.RawPath = ""
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create BYR request: %w", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Referer", (&url.URL{Scheme: c.baseURL.Scheme, Host: c.baseURL.Host}).String())
	request.AddCookie(&http.Cookie{Name: authTokenCookie, Value: credential.AuthToken})
	if credential.SessionID != "" {
		request.AddCookie(&http.Cookie{Name: sessionIDCookie, Value: credential.SessionID})
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send BYR request: %w", err)
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		response.Body.Close()
		return nil, ErrSessionExpired
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		response.Body.Close()
		statusErr := fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
		if netretry.TransientStatus(response.StatusCode) {
			statusErr = netretry.Transient(statusErr)
		}
		return nil, statusErr
	}

	return response, nil
}

func (c *Client) currentCredential() (Credential, uint64) {
	c.credentialMu.RLock()
	defer c.credentialMu.RUnlock()
	return c.credential, c.credentialGeneration
}

func (c *Client) sessionRefreshLimit() int {
	c.credentialMu.RLock()
	defer c.credentialMu.RUnlock()
	return c.maxSessionRefreshes
}

func (c *Client) consumeInitialFallbacks() {
	c.credentialMu.Lock()
	defer c.credentialMu.Unlock()
	if c.maxSessionRefreshes > 1 {
		c.maxSessionRefreshes = 1
	}
}

func (c *Client) refreshSession(ctx context.Context, expiredGeneration uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	_, currentGeneration := c.currentCredential()
	if currentGeneration != expiredGeneration {
		return nil
	}
	credential, err := c.refreshCredential(ctx)
	if err != nil {
		return err
	}
	credential, err = normalizeCredential(credential)
	if err != nil {
		return err
	}
	c.credentialMu.Lock()
	c.credential = credential
	c.credentialGeneration++
	c.credentialMu.Unlock()
	return nil
}

func validTorrentID(torrentID domain.TorrentID) bool {
	value := strings.TrimSpace(string(torrentID))
	if value == "" || value != string(torrentID) {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}
