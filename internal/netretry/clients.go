package netretry

import (
	"context"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

var _ port.Tracker = (*Tracker)(nil)
var _ port.TorrentClient = (*TorrentClient)(nil)

// Tracker retries each tracker operation independently.
type Tracker struct {
	delegate port.Tracker
	policy   Policy
}

func NewTracker(delegate port.Tracker, policy Policy) *Tracker {
	return &Tracker{delegate: delegate, policy: policy}
}

func (c *Tracker) ListTopFree(ctx context.Context) ([]domain.Candidate, error) {
	return DoValue(ctx, c.policy, "list BYR TopFree torrents", func() ([]domain.Candidate, error) {
		return c.delegate.ListTopFree(ctx)
	})
}

func (c *Tracker) DownloadURL(ctx context.Context, id domain.TorrentID) (string, error) {
	return DoValue(ctx, c.policy, "resolve BYR torrent download URL", func() (string, error) {
		return c.delegate.DownloadURL(ctx, id)
	})
}

// TorrentClient retries each qBittorrent operation independently.
type TorrentClient struct {
	delegate port.TorrentClient
	policy   Policy
}

func NewTorrentClient(delegate port.TorrentClient, policy Policy) *TorrentClient {
	return &TorrentClient{delegate: delegate, policy: policy}
}

func (c *TorrentClient) FreeSpace(ctx context.Context, path string) (domain.DiskSpace, error) {
	return DoValue(ctx, c.policy, "check qBittorrent free space", func() (domain.DiskSpace, error) {
		return c.delegate.FreeSpace(ctx, path)
	})
}

func (c *TorrentClient) ListAll(ctx context.Context) ([]domain.ManagedTorrent, error) {
	return DoValue(ctx, c.policy, "list qBittorrent torrents", func() ([]domain.ManagedTorrent, error) {
		return c.delegate.ListAll(ctx)
	})
}

func (c *TorrentClient) ListByTag(ctx context.Context, tag string) ([]domain.ManagedTorrent, error) {
	return DoValue(ctx, c.policy, "list qBittorrent torrents by tag", func() ([]domain.ManagedTorrent, error) {
		return c.delegate.ListByTag(ctx, tag)
	})
}

func (c *TorrentClient) AddTags(ctx context.Context, hashes []domain.TorrentHash, tags []string) error {
	return c.policy.Do(ctx, "add qBittorrent tags", func() error { return c.delegate.AddTags(ctx, hashes, tags) })
}

func (c *TorrentClient) RemoveTags(ctx context.Context, hashes []domain.TorrentHash, tags []string) error {
	return c.policy.Do(ctx, "remove qBittorrent tags", func() error { return c.delegate.RemoveTags(ctx, hashes, tags) })
}

func (c *TorrentClient) Add(ctx context.Context, request port.AddTorrentRequest) error {
	return c.policy.Do(ctx, "add qBittorrent torrent", func() error { return c.delegate.Add(ctx, request) })
}

func (c *TorrentClient) Reannounce(ctx context.Context, hashes []domain.TorrentHash) error {
	return c.policy.Do(ctx, "reannounce qBittorrent torrents", func() error { return c.delegate.Reannounce(ctx, hashes) })
}

func (c *TorrentClient) Pause(ctx context.Context, hashes []domain.TorrentHash) error {
	return c.policy.Do(ctx, "stop qBittorrent torrents", func() error { return c.delegate.Pause(ctx, hashes) })
}

func (c *TorrentClient) Delete(ctx context.Context, hashes []domain.TorrentHash, deleteFiles bool) error {
	return c.policy.Do(ctx, "delete qBittorrent torrents", func() error { return c.delegate.Delete(ctx, hashes, deleteFiles) })
}
