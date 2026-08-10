package byr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

const maxPageSize = 4 << 20

var (
	// ErrSessionExpired indicates that BYR no longer accepts the supplied cookies.
	ErrSessionExpired = errors.New("BYR session expired")
	// ErrInvalidTorrentPage indicates that the torrent page cannot be parsed safely.
	ErrInvalidTorrentPage = errors.New("invalid BYR torrent page")
	// ErrTorrentHashNotFound indicates that a details page does not contain a torrent hash.
	ErrTorrentHashNotFound = errors.New("BYR torrent hash not found")
)

// Client accesses authenticated BYR tracker pages.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	credential Credential

	hashMu    sync.RWMutex
	hashCache map[domain.TorrentID]domain.TorrentHash
}

// NewClient creates an authenticated client that shares this authenticator's HTTP settings.
func (a *Authenticator) NewClient(credential Credential) (*Client, error) {
	credential.AuthToken = strings.TrimSpace(credential.AuthToken)
	credential.SessionID = strings.TrimSpace(credential.SessionID)
	if credential.AuthToken == "" {
		return nil, ErrAuthenticationFailed
	}

	return &Client{
		baseURL:    a.baseURL,
		httpClient: a.httpClient,
		credential: credential,
		hashCache:  make(map[domain.TorrentID]domain.TorrentHash),
	}, nil
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
	request.AddCookie(&http.Cookie{Name: authTokenCookie, Value: c.credential.AuthToken})
	if c.credential.SessionID != "" {
		request.AddCookie(&http.Cookie{Name: sessionIDCookie, Value: c.credential.SessionID})
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
		return nil, fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}

	return response, nil
}
