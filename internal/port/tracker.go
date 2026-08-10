// Package port defines the capabilities required from external systems.
package port

import (
	"context"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

// Tracker exposes torrent discovery and download access required by strategies.
type Tracker interface {
	ListTopFree(context.Context) ([]domain.Candidate, error)
	DownloadURL(context.Context, domain.TorrentID) (string, error)
}
