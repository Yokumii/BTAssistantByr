// Package port defines the capabilities required from external systems.
package port

import (
	"context"
	"errors"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

// ErrInvalidTrackerSnapshot means discovery could not produce a complete,
// trustworthy snapshot. Callers must discard old data until discovery succeeds.
var ErrInvalidTrackerSnapshot = errors.New("invalid tracker snapshot")

// Tracker exposes torrent discovery and download access required by strategies.
type Tracker interface {
	ListTopFree(context.Context) ([]domain.Candidate, error)
	DownloadURL(context.Context, domain.TorrentID) (string, error)
}
