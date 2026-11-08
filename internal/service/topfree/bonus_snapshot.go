package topfree

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

// BonusTorrent joins a tracker ID to a hash without relying on process history.
type BonusTorrent struct {
	ID          domain.TorrentID
	PublishedAt time.Time
	SizeBytes   int64
	Seeders     int
	Seeding     bool
}

// BonusSnapshot is replaced atomically only after all required reads succeed.
type BonusSnapshot struct {
	ObservedAt time.Time
	AccountA   float64
	SeedCount  int
	Torrents   map[domain.TorrentHash]BonusTorrent
}

// EnableBonusPriority selects the opt-in strategy before the coordinator runs.
func (c *Coordinator) EnableBonusPriority(source port.BonusTracker, interval time.Duration) error {
	if source == nil || interval <= 0 {
		return errors.New("bonus priority requires a tracker source and a positive refresh interval")
	}
	c.bonusSource = source
	c.bonusInterval = interval
	return nil
}

func (c *Coordinator) loadBonusSnapshot(ctx context.Context, candidates []domain.Candidate) (BonusSnapshot, error) {
	started := c.now()
	if cached := c.bonusSnapshot; cached != nil && !started.Before(cached.ObservedAt) && started.Sub(cached.ObservedAt) < c.bonusInterval {
		complete := true
		for _, candidate := range candidates {
			metadata, exists := cached.Torrents[canonicalHash(candidate.Hash)]
			if !exists || metadata.ID != candidate.ID || (!metadata.Seeding && metadata.Seeders != candidate.Seeders) {
				complete = false
				break
			}
		}
		if complete {
			if c.logger != nil {
				c.logger.DebugContext(ctx, "using cached TopFree bonus snapshot", "observed_at", cached.ObservedAt, "valid_until", cached.ObservedAt.Add(c.bonusInterval), "seeding_torrents", cached.SeedCount, "torrent_metadata", len(cached.Torrents))
			}
			return *cached, nil
		}
	}
	// A failed refresh must not leave the previous snapshot usable for writes.
	c.bonusSnapshot = nil
	if c.logger != nil {
		c.logger.DebugContext(ctx, "refreshing TopFree bonus snapshot", "candidates", len(candidates))
	}
	account, err := c.bonusSource.BonusAccount(ctx)
	if err != nil {
		return BonusSnapshot{}, fmt.Errorf("read bonus account: %w", err)
	}
	seeded, err := c.bonusSource.ListSeeding(ctx, account.UserID)
	if err != nil {
		return BonusSnapshot{}, fmt.Errorf("read bonus seeding list: %w", err)
	}
	snapshot := BonusSnapshot{
		ObservedAt: started,
		AccountA:   account.A,
		SeedCount:  len(seeded),
		Torrents:   make(map[domain.TorrentHash]BonusTorrent, len(seeded)+len(candidates)),
	}
	seenIDs := make(map[domain.TorrentID]bool)
	for _, torrent := range seeded {
		metadata, err := c.bonusSource.TorrentMetadata(ctx, torrent.ID)
		if err != nil {
			return BonusSnapshot{}, fmt.Errorf("read seeding metadata for %s: %w", torrent.ID, err)
		}
		hash := canonicalHash(metadata.Hash)
		if _, exists := snapshot.Torrents[hash]; exists || seenIDs[torrent.ID] {
			return BonusSnapshot{}, fmt.Errorf("%w: duplicate bonus seeding identity", port.ErrInvalidTrackerSnapshot)
		}
		seenIDs[torrent.ID] = true
		snapshot.Torrents[hash] = BonusTorrent{
			ID: torrent.ID, PublishedAt: metadata.PublishedAt,
			SizeBytes: torrent.SizeBytes, Seeders: torrent.Seeders, Seeding: true,
		}
	}
	for _, candidate := range candidates {
		hash := canonicalHash(candidate.Hash)
		if existing, exists := snapshot.Torrents[hash]; exists {
			if existing.ID != candidate.ID {
				return BonusSnapshot{}, fmt.Errorf("%w: conflicting bonus torrent identity", port.ErrInvalidTrackerSnapshot)
			}
			continue
		}
		metadata, err := c.bonusSource.TorrentMetadata(ctx, candidate.ID)
		if err != nil {
			return BonusSnapshot{}, fmt.Errorf("read candidate metadata for %s: %w", candidate.ID, err)
		}
		if canonicalHash(metadata.Hash) != hash || seenIDs[candidate.ID] {
			return BonusSnapshot{}, fmt.Errorf("%w: bonus candidate hash mismatch", port.ErrInvalidTrackerSnapshot)
		}
		snapshot.Torrents[hash] = BonusTorrent{
			ID: candidate.ID, PublishedAt: metadata.PublishedAt,
			SizeBytes: candidate.SizeBytes, Seeders: candidate.Seeders,
		}
	}
	if err := validateBonusSnapshot(snapshot, c.now(), c.bonusInterval); err != nil {
		return BonusSnapshot{}, err
	}
	c.bonusSnapshot = &snapshot
	if c.logger != nil {
		c.logger.DebugContext(ctx, "TopFree bonus snapshot refreshed", "observed_at", snapshot.ObservedAt, "valid_until", snapshot.ObservedAt.Add(c.bonusInterval), "seeding_torrents", snapshot.SeedCount, "torrent_metadata", len(snapshot.Torrents))
	}
	return snapshot, nil
}
