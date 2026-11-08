package domain

import "time"

// BonusAccount is the account-wide value reported by BYR's seeding bonus page.
type BonusAccount struct {
	UserID string
	A      float64
}

// SeededTorrent is one distinct torrent in the account's current seeding list.
type SeededTorrent struct {
	ID        TorrentID
	SizeBytes int64
	Seeders   int
}

// TorrentMetadata contains immutable tracker identity and publication data.
type TorrentMetadata struct {
	Hash        TorrentHash
	PublishedAt time.Time
}
