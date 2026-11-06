package port

import (
	"context"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

// BonusTracker keeps each additional tracker read independently retryable.
type BonusTracker interface {
	BonusAccount(context.Context) (domain.BonusAccount, error)
	ListSeeding(context.Context, string) ([]domain.SeededTorrent, error)
	TorrentMetadata(context.Context, domain.TorrentID) (domain.TorrentMetadata, error)
}
