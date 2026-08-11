package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const (
	maxTorrentListSize  = 16 << 20
	maxTextResponseSize = 64 << 10
)

var (
	// ErrSessionExpired indicates that qBittorrent rejected the session cookie.
	ErrSessionExpired = errors.New("qBittorrent session expired")
	// ErrInvalidResponse indicates that qBittorrent returned malformed JSON.
	ErrInvalidResponse = errors.New("invalid qBittorrent response")
	// ErrTagRequired indicates that a tag filter or mutation has no tag.
	ErrTagRequired = errors.New("qBittorrent tag is required")
	// ErrHashesRequired indicates that a mutation has no torrent hashes.
	ErrHashesRequired = errors.New("qBittorrent torrent hashes are required")
	// ErrInvalidTorrentHash indicates that a hash cannot safely be sent to qBittorrent.
	ErrInvalidTorrentHash = errors.New("invalid qBittorrent torrent hash")
	// ErrInvalidSourceURL indicates that an add request has no supported torrent URL.
	ErrInvalidSourceURL = errors.New("invalid qBittorrent torrent source URL")
	// ErrFreeSpaceUnsupported indicates that this Web API cannot inspect a custom save path.
	ErrFreeSpaceUnsupported = errors.New("qBittorrent Web API cannot inspect free space at the configured save path")
)

var torrentHashPattern = regexp.MustCompile(`(?i)^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

var _ port.TorrentClient = (*Client)(nil)

// Client accesses authenticated qBittorrent Web API v2 endpoints.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	credential Credential
}

// NewClient creates an authenticated qBittorrent client.
func (a *Authenticator) NewClient(credential Credential) (*Client, error) {
	validated, err := NewCredentialWithCookie(credential.CookieName, credential.SID)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:    a.baseURL,
		httpClient: a.httpClient,
		credential: validated,
	}, nil
}

// FreeSpace returns free bytes measured by qBittorrent at the effective save path.
// WebAPI 2.15.2+ supports arbitrary paths. Older APIs are safe only for the
// qBittorrent default save path exposed by sync/maindata.
func (c *Client) FreeSpace(ctx context.Context, configuredPath string) (domain.DiskSpace, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	version, err := c.webAPIVersion(ctx)
	if err != nil {
		return domain.DiskSpace{}, fmt.Errorf("get qBittorrent Web API version: %w", err)
	}

	effectivePath := configuredPath
	if effectivePath == "" {
		effectivePath, err = c.defaultSavePath(ctx)
		if err != nil {
			return domain.DiskSpace{}, fmt.Errorf("get qBittorrent default save path: %w", err)
		}
	}

	if version.atLeast(2, 15, 2) {
		freeBytes, err := c.freeSpaceAtPath(ctx, effectivePath)
		if err != nil {
			return domain.DiskSpace{}, fmt.Errorf("get qBittorrent free space at %q: %w", effectivePath, err)
		}
		return domain.DiskSpace{Path: effectivePath, FreeBytes: freeBytes}, nil
	}

	if configuredPath != "" {
		defaultPath, err := c.defaultSavePath(ctx)
		if err != nil {
			return domain.DiskSpace{}, fmt.Errorf("get qBittorrent default save path: %w", err)
		}
		if !sameSavePath(configuredPath, defaultPath) {
			return domain.DiskSpace{}, fmt.Errorf("%w: WebAPI %s only reports its default path %q, configured path is %q", ErrFreeSpaceUnsupported, version, defaultPath, configuredPath)
		}
		effectivePath = defaultPath
	}

	freeBytes, err := c.defaultFreeSpace(ctx)
	if err != nil {
		return domain.DiskSpace{}, fmt.Errorf("get qBittorrent default-path free space: %w", err)
	}
	confirmedPath, err := c.defaultSavePath(ctx)
	if err != nil {
		return domain.DiskSpace{}, fmt.Errorf("confirm qBittorrent default save path: %w", err)
	}
	if !sameSavePath(effectivePath, confirmedPath) {
		return domain.DiskSpace{}, fmt.Errorf("%w: qBittorrent default save path changed from %q to %q during free-space check", ErrFreeSpaceUnsupported, effectivePath, confirmedPath)
	}
	return domain.DiskSpace{Path: effectivePath, FreeBytes: freeBytes}, nil
}

type apiVersion struct {
	major int
	minor int
	patch int
}

func (version apiVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
}

func (version apiVersion) atLeast(major, minor, patch int) bool {
	if version.major != major {
		return version.major > major
	}
	if version.minor != minor {
		return version.minor > minor
	}
	return version.patch >= patch
}

func (c *Client) webAPIVersion(ctx context.Context) (apiVersion, error) {
	value, err := c.getText(ctx, "api/v2/app/webapiVersion", nil)
	if err != nil {
		return apiVersion{}, err
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return apiVersion{}, ErrInvalidResponse
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		numbers[index], err = strconv.Atoi(part)
		if err != nil || numbers[index] < 0 {
			return apiVersion{}, ErrInvalidResponse
		}
	}
	return apiVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}, nil
}

func (c *Client) defaultSavePath(ctx context.Context) (string, error) {
	value, err := c.getText(ctx, "api/v2/app/defaultSavePath", nil)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", ErrInvalidResponse
	}
	return value, nil
}

func (c *Client) freeSpaceAtPath(ctx context.Context, path string) (int64, error) {
	value, err := c.getText(ctx, "api/v2/app/getFreeSpaceAtPathAction", url.Values{"path": {path}})
	if err != nil {
		return 0, err
	}
	return parseFreeBytes(value)
}

func (c *Client) defaultFreeSpace(ctx context.Context) (int64, error) {
	response, err := c.request(ctx, http.MethodGet, "api/v2/sync/maindata", url.Values{"rid": {"0"}}, "", nil)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	var payload struct {
		ServerState struct {
			FreeSpaceOnDisk *int64 `json:"free_space_on_disk"`
		} `json:"server_state"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxTorrentListSize))
	if err := decoder.Decode(&payload); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return 0, err
	}
	if payload.ServerState.FreeSpaceOnDisk == nil || *payload.ServerState.FreeSpaceOnDisk < 0 {
		return 0, ErrInvalidResponse
	}
	return *payload.ServerState.FreeSpaceOnDisk, nil
}

func (c *Client) getText(ctx context.Context, path string, query url.Values) (string, error) {
	response, err := c.request(ctx, http.MethodGet, path, query, "", nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxTextResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if len(contents) > maxTextResponseSize {
		return "", ErrInvalidResponse
	}
	return strings.TrimSpace(string(contents)), nil
}

func parseFreeBytes(value string) (int64, error) {
	freeBytes, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || freeBytes < 0 {
		return 0, ErrInvalidResponse
	}
	return freeBytes, nil
}

func sameSavePath(first, second string) bool {
	normalize := func(value string) string {
		value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
		if len(value) > 1 {
			value = strings.TrimRight(value, "/")
		}
		return value
	}
	return normalize(first) == normalize(second)
}

// ListAll returns every torrent visible to the authenticated qBittorrent session.
func (c *Client) ListAll(ctx context.Context) ([]domain.ManagedTorrent, error) {
	return c.list(ctx, nil)
}

// ListByTag returns torrents carrying exactly the requested qBittorrent tag.
func (c *Client) ListByTag(ctx context.Context, tag string) ([]domain.ManagedTorrent, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, ErrTagRequired
	}
	return c.list(ctx, url.Values{"tag": {tag}})
}

func (c *Client) list(ctx context.Context, query url.Values) ([]domain.ManagedTorrent, error) {
	response, err := c.request(ctx, http.MethodGet, "api/v2/torrents/info", query, "", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload []struct {
		Hash      string `json:"hash"`
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		Completed int64  `json:"completed"`
		AddedOn   int64  `json:"added_on"`
		SavePath  string `json:"save_path"`
		Tags      string `json:"tags"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxTorrentListSize))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}

	torrents := make([]domain.ManagedTorrent, 0, len(payload))
	for _, item := range payload {
		if !torrentHashPattern.MatchString(item.Hash) || strings.TrimSpace(item.Name) == "" || item.Size < 0 || item.Completed < 0 || item.Completed > item.Size || item.AddedOn < 0 {
			return nil, ErrInvalidResponse
		}
		torrents = append(torrents, domain.ManagedTorrent{
			Hash:           domain.TorrentHash(strings.ToLower(item.Hash)),
			Name:           item.Name,
			SizeBytes:      item.Size,
			CompletedBytes: item.Completed,
			AddedAt:        time.Unix(item.AddedOn, 0),
			SavePath:       item.SavePath,
			Tags:           splitTags(item.Tags),
		})
	}
	return torrents, nil
}

// AddTags adds tags to the selected torrents.
func (c *Client) AddTags(ctx context.Context, hashes []domain.TorrentHash, tags []string) error {
	hashValue, err := joinHashes(hashes)
	if err != nil {
		return err
	}
	tagValue, err := joinTags(tags)
	if err != nil {
		return err
	}
	return c.postForm(ctx, "api/v2/torrents/addTags", url.Values{
		"hashes": {hashValue},
		"tags":   {tagValue},
	})
}

// RemoveTags removes tags from the selected torrents.
func (c *Client) RemoveTags(ctx context.Context, hashes []domain.TorrentHash, tags []string) error {
	hashValue, err := joinHashes(hashes)
	if err != nil {
		return err
	}
	tagValue, err := joinTags(tags)
	if err != nil {
		return err
	}
	return c.postForm(ctx, "api/v2/torrents/removeTags", url.Values{
		"hashes": {hashValue},
		"tags":   {tagValue},
	})
}

// Add submits a torrent URL to qBittorrent.
func (c *Client) Add(ctx context.Context, addRequest port.AddTorrentRequest) error {
	if !validSourceURL(addRequest.SourceURL) {
		return ErrInvalidSourceURL
	}
	tags := ""
	var err error
	if len(addRequest.Tags) > 0 {
		tags, err = joinTags(addRequest.Tags)
		if err != nil {
			return err
		}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"urls":     strings.TrimSpace(addRequest.SourceURL),
		"savepath": addRequest.SavePath,
		"tags":     tags,
	}
	for _, key := range []string{"urls", "savepath", "tags"} {
		if fields[key] == "" && key != "urls" {
			continue
		}
		if err := writer.WriteField(key, fields[key]); err != nil {
			return fmt.Errorf("encode qBittorrent add request: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("encode qBittorrent add request: %w", err)
	}
	response, err := c.request(ctx, http.MethodPost, "api/v2/torrents/add", nil, writer.FormDataContentType(), &body)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// Reannounce forces the selected torrents to contact their trackers.
func (c *Client) Reannounce(ctx context.Context, hashes []domain.TorrentHash) error {
	return c.postHashes(ctx, "api/v2/torrents/reannounce", hashes, nil)
}

// Pause stops the selected torrents using the qBittorrent 5.x endpoint.
func (c *Client) Pause(ctx context.Context, hashes []domain.TorrentHash) error {
	return c.postHashes(ctx, "api/v2/torrents/stop", hashes, nil)
}

// Delete removes the selected torrents and optionally their downloaded data.
func (c *Client) Delete(ctx context.Context, hashes []domain.TorrentHash, deleteFiles bool) error {
	return c.postHashes(ctx, "api/v2/torrents/delete", hashes, url.Values{
		"deleteFiles": {strconv.FormatBool(deleteFiles)},
	})
}

func (c *Client) postHashes(ctx context.Context, path string, hashes []domain.TorrentHash, extra url.Values) error {
	hashValue, err := joinHashes(hashes)
	if err != nil {
		return err
	}
	form := url.Values{"hashes": {hashValue}}
	for key, values := range extra {
		form[key] = values
	}
	return c.postForm(ctx, path, form)
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) error {
	response, err := c.request(
		ctx,
		http.MethodPost,
		path,
		nil,
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

func (c *Client) request(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	contentType string,
	body io.Reader,
) (*http.Response, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + path
	endpoint.RawPath = ""
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Referer", (&url.URL{Scheme: c.baseURL.Scheme, Host: c.baseURL.Host}).String())
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.AddCookie(&http.Cookie{Name: c.credential.CookieName, Value: c.credential.SID})

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send qBittorrent request: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		return nil, ErrSessionExpired
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		response.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}
	return response, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidResponse
	}
	return nil
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		tags = append(tags, strings.TrimSpace(part))
	}
	return tags
}

func joinHashes(hashes []domain.TorrentHash) (string, error) {
	if len(hashes) == 0 {
		return "", ErrHashesRequired
	}
	values := make([]string, len(hashes))
	for index, hash := range hashes {
		value := strings.TrimSpace(string(hash))
		if !torrentHashPattern.MatchString(value) {
			return "", ErrInvalidTorrentHash
		}
		values[index] = strings.ToLower(value)
	}
	return strings.Join(values, "|"), nil
}

func joinTags(tags []string) (string, error) {
	if len(tags) == 0 {
		return "", ErrTagRequired
	}
	values := make([]string, len(tags))
	for index, tag := range tags {
		value := strings.TrimSpace(tag)
		if value == "" || strings.Contains(value, ",") {
			return "", ErrTagRequired
		}
		values[index] = value
	}
	return strings.Join(values, ","), nil
}

func validSourceURL(value string) bool {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Host != ""
	case "magnet", "bc":
		return strings.TrimPrefix(value, parsed.Scheme+":") != ""
	default:
		return false
	}
}
