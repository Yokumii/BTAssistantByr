package port

import (
	"context"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

// AddTorrentRequest describes a torrent client add operation.
type AddTorrentRequest struct {
	SourceURL string
	SavePath  string
	Tags      []string
}

// TorrentClient exposes the client operations required by strategies.
type TorrentClient interface {
	ListAll(context.Context) ([]domain.ManagedTorrent, error)
	ListByTag(context.Context, string) ([]domain.ManagedTorrent, error)
	AddTags(context.Context, []domain.TorrentHash, []string) error
	RemoveTags(context.Context, []domain.TorrentHash, []string) error
	Add(context.Context, AddTorrentRequest) error
	Reannounce(context.Context, []domain.TorrentHash) error
	Pause(context.Context, []domain.TorrentHash) error
	Delete(context.Context, []domain.TorrentHash, bool) error
}
