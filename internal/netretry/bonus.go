package netretry

import (
	"context"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

// BonusTracker retries individual reads, never an assembled planning cycle.
type BonusTracker struct {
	delegate port.BonusTracker
	policy   Policy
}

func NewBonusTracker(delegate port.BonusTracker, policy Policy) *BonusTracker {
	return &BonusTracker{delegate: delegate, policy: policy}
}

func (c *BonusTracker) BonusAccount(ctx context.Context) (domain.BonusAccount, error) {
	return DoValue(ctx, c.policy, "read BYR bonus account", func() (domain.BonusAccount, error) {
		return c.delegate.BonusAccount(ctx)
	})
}

func (c *BonusTracker) ListSeeding(ctx context.Context, userID string) ([]domain.SeededTorrent, error) {
	return DoValue(ctx, c.policy, "read BYR seeding list", func() ([]domain.SeededTorrent, error) {
		return c.delegate.ListSeeding(ctx, userID)
	})
}

func (c *BonusTracker) TorrentMetadata(ctx context.Context, id domain.TorrentID) (domain.TorrentMetadata, error) {
	return DoValue(ctx, c.policy.withLogAttrs("torrent_id", id), "read BYR publication metadata", func() (domain.TorrentMetadata, error) {
		return c.delegate.TorrentMetadata(ctx, id)
	})
}
