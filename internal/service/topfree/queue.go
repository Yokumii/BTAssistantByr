package topfree

import "github.com/Yokumii/BTAssistantByr/internal/domain"

// downloadQueue is reconciled from complete tracker and qBittorrent snapshots
// every cycle. pending prevents duplicate adds before qBittorrent exposes a write.
type downloadQueue struct {
	entries map[domain.TorrentHash]domain.Candidate
	pending domain.TorrentHash
}

type queueSnapshot struct {
	candidates      []domain.Candidate
	removed         []domain.TorrentHash
	downloadBlocked bool
}

func (q *downloadQueue) sync(currentCandidates, eligibleCandidates []domain.Candidate, managed []domain.ManagedTorrent) (queueSnapshot, error) {
	normalizedCandidates, err := normalizeCandidates(currentCandidates)
	if err != nil {
		return queueSnapshot{}, err
	}
	normalizedEligible, err := normalizeCandidates(eligibleCandidates)
	if err != nil {
		return queueSnapshot{}, err
	}
	normalizedManaged, err := normalizeManaged(managed)
	if err != nil {
		return queueSnapshot{}, err
	}

	current := make(map[domain.TorrentHash]domain.Candidate, len(normalizedCandidates))
	for _, candidate := range normalizedCandidates {
		current[candidate.Hash] = candidate
	}
	eligible := make(map[domain.TorrentHash]domain.Candidate, len(normalizedEligible))
	for _, candidate := range normalizedEligible {
		if _, exists := current[candidate.Hash]; !exists {
			return queueSnapshot{}, ErrInvalidCandidate
		}
		eligible[candidate.Hash] = candidate
	}
	managedByHash := make(map[domain.TorrentHash]domain.ManagedTorrent, len(normalizedManaged))
	for _, torrent := range normalizedManaged {
		managedByHash[torrent.Hash] = torrent
	}

	if _, visible := managedByHash[q.pending]; q.pending != "" && visible {
		q.pending = ""
	}
	if _, stillTopFree := current[q.pending]; q.pending != "" && !stillTopFree {
		q.pending = ""
	}

	removed := make([]domain.TorrentHash, 0)
	for hash := range q.entries {
		if _, exists := current[hash]; !exists {
			removed = append(removed, hash)
		}
	}
	sortHashes(removed)

	q.entries = make(map[domain.TorrentHash]domain.Candidate, len(eligible))
	downloadBlocked := q.pending != ""
	for hash := range current {
		if hash == q.pending {
			continue
		}
		if torrent, exists := managedByHash[hash]; exists {
			if tagsInManagementScope(torrent.Tags) && isIncomplete(torrent) {
				downloadBlocked = true
			}
			continue
		}
		if candidate, allowed := eligible[hash]; allowed {
			q.entries[hash] = candidate
		}
	}

	queued := make([]domain.Candidate, 0, len(q.entries))
	for _, candidate := range q.entries {
		queued = append(queued, candidate)
	}
	return queueSnapshot{candidates: queued, removed: removed, downloadBlocked: downloadBlocked}, nil
}

func (q *downloadQueue) markSubmitted(hash domain.TorrentHash) {
	hash = canonicalHash(hash)
	delete(q.entries, hash)
	q.pending = hash
}
