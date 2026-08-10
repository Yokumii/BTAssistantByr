package topfree

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

const (
	// ManagedTag identifies torrents owned by the TopFree strategy.
	ManagedTag = "sc"
	// DeleteTag marks managed torrents that are no longer TopFree.
	DeleteTag = "toDel"
)

var (
	// ErrInvalidDiskLimit indicates that the strategy has no usable capacity limit.
	ErrInvalidDiskLimit = errors.New("TopFree disk limit must be positive")
	// ErrInvalidCandidate indicates that a TopFree candidate is incomplete or unsafe.
	ErrInvalidCandidate = errors.New("invalid TopFree candidate")
	// ErrInvalidManagedTorrent indicates that a managed torrent is incomplete or unsafe.
	ErrInvalidManagedTorrent = errors.New("invalid managed torrent")
	// ErrDuplicateCandidate indicates that a candidate hash or ID appears more than once.
	ErrDuplicateCandidate = errors.New("duplicate TopFree candidate")
	// ErrDuplicateManagedTorrent indicates that a client hash appears more than once.
	ErrDuplicateManagedTorrent = errors.New("duplicate managed torrent")
	// ErrDiskUsageOverflow indicates that managed torrent sizes cannot fit in int64.
	ErrDiskUsageOverflow = errors.New("TopFree disk usage overflows int64")
)

var planHashPattern = regexp.MustCompile(`(?i)^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

// SkipReason explains why a candidate is not part of the download plan.
type SkipReason string

const (
	// SkipInsufficientSpace means the candidate cannot fit even after safe cleanup.
	SkipInsufficientSpace SkipReason = "insufficient_space"
)

// DownloadPlan describes cleanup that must complete before adding one candidate.
type DownloadPlan struct {
	Candidate    domain.Candidate
	DeleteBefore []domain.ManagedTorrent
}

// SkippedCandidate records a capacity decision without hiding the candidate.
type SkippedCandidate struct {
	Candidate      domain.Candidate
	Reason         SkipReason
	AvailableBytes int64
}

// Plan is a deterministic, side-effect-free set of TopFree decisions.
type Plan struct {
	TagAsManaged            []domain.TorrentHash
	UnmarkForDeletion       []domain.TorrentHash
	MarkForDeletion         []domain.TorrentHash
	Downloads               []DownloadPlan
	Skipped                 []SkippedCandidate
	CurrentDiskUsageBytes   int64
	ProjectedDiskUsageBytes int64
}

// BuildPlan computes TopFree tag, cleanup, and download decisions without calling external systems.
func BuildPlan(
	maxDiskUsageBytes int64,
	candidates []domain.Candidate,
	managed []domain.ManagedTorrent,
) (Plan, error) {
	if maxDiskUsageBytes <= 0 {
		return Plan{}, ErrInvalidDiskLimit
	}

	normalizedCandidates, err := normalizeCandidates(candidates)
	if err != nil {
		return Plan{}, err
	}
	normalizedManaged, err := normalizeManaged(managed)
	if err != nil {
		return Plan{}, err
	}

	topFreeHashes := make(map[domain.TorrentHash]struct{}, len(normalizedCandidates))
	for _, candidate := range normalizedCandidates {
		topFreeHashes[candidate.Hash] = struct{}{}
	}
	managedByHash := make(map[domain.TorrentHash]domain.ManagedTorrent, len(normalizedManaged))
	for _, torrent := range normalizedManaged {
		managedByHash[torrent.Hash] = torrent
	}

	plan := Plan{}
	stale := make([]domain.ManagedTorrent, 0)
	alreadyDeleteTagged := make(map[domain.TorrentHash]bool)
	for _, torrent := range normalizedManaged {
		_, isTopFree := topFreeHashes[torrent.Hash]
		isManaged := hasTag(torrent.Tags, ManagedTag)
		isDeleteTagged := hasTag(torrent.Tags, DeleteTag)
		if !isManaged && !isTopFree {
			continue
		}
		if torrent.SizeBytes > math.MaxInt64-plan.CurrentDiskUsageBytes {
			return Plan{}, ErrDiskUsageOverflow
		}
		plan.CurrentDiskUsageBytes += torrent.SizeBytes

		if isTopFree {
			if !isManaged {
				plan.TagAsManaged = append(plan.TagAsManaged, torrent.Hash)
			}
			if isDeleteTagged {
				plan.UnmarkForDeletion = append(plan.UnmarkForDeletion, torrent.Hash)
			}
			continue
		}

		stale = append(stale, torrent)
		alreadyDeleteTagged[torrent.Hash] = isDeleteTagged
	}

	newCandidates := make([]domain.Candidate, 0, len(normalizedCandidates))
	for _, candidate := range normalizedCandidates {
		if _, exists := managedByHash[candidate.Hash]; !exists {
			newCandidates = append(newCandidates, candidate)
		}
	}
	sortCandidates(newCandidates)
	sort.Slice(stale, func(left, right int) bool {
		if stale[left].AddedAt.Equal(stale[right].AddedAt) {
			return stale[left].Hash < stale[right].Hash
		}
		return stale[left].AddedAt.Before(stale[right].AddedAt)
	})

	usage := plan.CurrentDiskUsageBytes
	remainingStale := stale
	deleted := make(map[domain.TorrentHash]struct{})
	for _, candidate := range newCandidates {
		reclaimable := sumManagedSizes(remainingStale)
		required, fits := requiredCleanup(maxDiskUsageBytes, usage, candidate.SizeBytes, reclaimable)
		if !fits {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{
				Candidate:      candidate,
				Reason:         SkipInsufficientSpace,
				AvailableBytes: availableAfterCleanup(maxDiskUsageBytes, usage, reclaimable),
			})
			continue
		}

		download := DownloadPlan{Candidate: candidate}
		freed := int64(0)
		deleteCount := 0
		for deleteCount < len(remainingStale) && freed < required {
			freed += remainingStale[deleteCount].SizeBytes
			download.DeleteBefore = append(download.DeleteBefore, remainingStale[deleteCount])
			deleted[remainingStale[deleteCount].Hash] = struct{}{}
			deleteCount++
		}
		remainingStale = remainingStale[deleteCount:]
		usage = usage - freed + candidate.SizeBytes
		plan.Downloads = append(plan.Downloads, download)
	}

	for _, torrent := range stale {
		if _, willDelete := deleted[torrent.Hash]; willDelete || alreadyDeleteTagged[torrent.Hash] {
			continue
		}
		plan.MarkForDeletion = append(plan.MarkForDeletion, torrent.Hash)
	}
	sortHashes(plan.TagAsManaged)
	sortHashes(plan.UnmarkForDeletion)
	sortHashes(plan.MarkForDeletion)
	plan.ProjectedDiskUsageBytes = usage
	return plan, nil
}

func normalizeCandidates(candidates []domain.Candidate) ([]domain.Candidate, error) {
	normalized := make([]domain.Candidate, len(candidates))
	hashes := make(map[domain.TorrentHash]struct{}, len(candidates))
	ids := make(map[domain.TorrentID]struct{}, len(candidates))
	for index, candidate := range candidates {
		candidate.ID = domain.TorrentID(strings.TrimSpace(string(candidate.ID)))
		candidate.Hash = canonicalHash(candidate.Hash)
		candidate.Name = strings.TrimSpace(candidate.Name)
		if !validTorrentID(candidate.ID) || !planHashPattern.MatchString(string(candidate.Hash)) || candidate.Name == "" || candidate.SizeBytes <= 0 || candidate.Seeders < 0 || candidate.Leechers < 0 || candidate.FreeUntil.IsZero() {
			return nil, fmt.Errorf("%w at index %d", ErrInvalidCandidate, index)
		}
		if _, exists := hashes[candidate.Hash]; exists {
			return nil, fmt.Errorf("%w: hash %s", ErrDuplicateCandidate, candidate.Hash)
		}
		if _, exists := ids[candidate.ID]; exists {
			return nil, fmt.Errorf("%w: ID %s", ErrDuplicateCandidate, candidate.ID)
		}
		hashes[candidate.Hash] = struct{}{}
		ids[candidate.ID] = struct{}{}
		normalized[index] = candidate
	}
	return normalized, nil
}

func normalizeManaged(managed []domain.ManagedTorrent) ([]domain.ManagedTorrent, error) {
	normalized := make([]domain.ManagedTorrent, len(managed))
	hashes := make(map[domain.TorrentHash]struct{}, len(managed))
	for index, torrent := range managed {
		torrent.Hash = canonicalHash(torrent.Hash)
		torrent.Name = strings.TrimSpace(torrent.Name)
		if !planHashPattern.MatchString(string(torrent.Hash)) || torrent.Name == "" || torrent.SizeBytes < 0 || torrent.AddedAt.IsZero() {
			return nil, fmt.Errorf("%w at index %d", ErrInvalidManagedTorrent, index)
		}
		if _, exists := hashes[torrent.Hash]; exists {
			return nil, fmt.Errorf("%w: hash %s", ErrDuplicateManagedTorrent, torrent.Hash)
		}
		hashes[torrent.Hash] = struct{}{}
		normalized[index] = torrent
	}
	return normalized, nil
}

func sortCandidates(candidates []domain.Candidate) {
	sort.Slice(candidates, func(left, right int) bool {
		first := candidates[left]
		second := candidates[right]
		if (first.Seeders > 0) != (second.Seeders > 0) {
			return first.Seeders > 0
		}
		if first.Seeders > 0 {
			firstRatio := float64(first.Leechers) / float64(first.Seeders)
			secondRatio := float64(second.Leechers) / float64(second.Seeders)
			if firstRatio != secondRatio {
				return firstRatio > secondRatio
			}
		}
		if first.Leechers != second.Leechers {
			return first.Leechers > second.Leechers
		}
		if first.Seeders != second.Seeders {
			return first.Seeders < second.Seeders
		}
		return first.Hash < second.Hash
	})
}

func requiredCleanup(maximum, usage, candidateSize, reclaimable int64) (int64, bool) {
	if candidateSize > maximum {
		return 0, false
	}
	targetUsage := maximum - candidateSize
	if usage <= targetUsage {
		return 0, true
	}
	required := usage - targetUsage
	return required, reclaimable >= required
}

func availableAfterCleanup(maximum, usage, reclaimable int64) int64 {
	minimumUsage := usage - reclaimable
	if minimumUsage >= maximum {
		return 0
	}
	return maximum - minimumUsage
}

func sumManagedSizes(torrents []domain.ManagedTorrent) int64 {
	total := int64(0)
	for _, torrent := range torrents {
		total += torrent.SizeBytes
	}
	return total
}

func hasTag(tags []string, expected string) bool {
	for _, tag := range tags {
		if strings.TrimSpace(tag) == expected {
			return true
		}
	}
	return false
}

func canonicalHash(hash domain.TorrentHash) domain.TorrentHash {
	return domain.TorrentHash(strings.ToLower(strings.TrimSpace(string(hash))))
}

func validTorrentID(id domain.TorrentID) bool {
	_, err := strconv.ParseUint(string(id), 10, 64)
	return id != "" && err == nil
}

func sortHashes(hashes []domain.TorrentHash) {
	sort.Slice(hashes, func(left, right int) bool {
		return hashes[left] < hashes[right]
	})
}
