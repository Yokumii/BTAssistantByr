package topfree

import (
	"strings"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func candidatesWithinSizeLimit(candidates []domain.Candidate, maximumBytes int64) ([]domain.Candidate, []domain.Candidate, error) {
	normalized, err := normalizeCandidates(candidates)
	if err != nil {
		return nil, nil, err
	}
	if maximumBytes == 0 {
		return normalized, nil, nil
	}
	eligible := make([]domain.Candidate, 0, len(normalized))
	oversized := make([]domain.Candidate, 0)
	for _, candidate := range normalized {
		if candidate.SizeBytes > maximumBytes {
			oversized = append(oversized, candidate)
			continue
		}
		eligible = append(eligible, candidate)
	}
	return eligible, oversized, nil
}

func torrentsInManagementScope(torrents []domain.ManagedTorrent) []domain.ManagedTorrent {
	managed := make([]domain.ManagedTorrent, 0, len(torrents))
	for _, torrent := range torrents {
		if tagsInManagementScope(torrent.Tags) {
			managed = append(managed, torrent)
		}
	}
	return managed
}

func tagsInManagementScope(tags []string) bool {
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if tag != ManagedTag && tag != DeleteTag {
			return false
		}
	}
	return true
}
