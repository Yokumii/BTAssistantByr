// Package domain contains types shared by the strategy and external adapters.
package domain

import "time"

// TorrentID is the tracker-local identifier of a torrent.
type TorrentID string

// TorrentHash is the content hash used by torrent clients.
type TorrentHash string

// Candidate is a torrent discovered from the tracker for strategy evaluation.
type Candidate struct {
	ID        TorrentID
	Hash      TorrentHash
	Name      string
	SizeBytes int64
	Seeders   int
	Leechers  int
	FreeUntil time.Time
}

// ManagedTorrent is a torrent already known by the local torrent client.
type ManagedTorrent struct {
	Hash            TorrentHash
	Name            string
	SizeBytes       int64
	CompletedBytes  int64
	AmountLeftBytes int64
	State           string
	AddedAt         time.Time
	SavePath        string
	Tags            []string
}

// DiskSpace reports free bytes for the effective qBittorrent download path.
type DiskSpace struct {
	Path      string
	FreeBytes int64
}
