package topfree

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

// BonusEstimate is a current-snapshot estimate, assuming the new download completes.
type BonusEstimate struct {
	ObservedAt             time.Time
	ValidUntil             time.Time
	ReportedA              float64
	BeforeA                float64
	AfterA                 float64
	BeforePerHour          float64
	AfterPerHour           float64
	AddedA                 float64
	RemovedA               float64
	NetPerHour             float64
	ReplacementGainPerHour float64
}

func bonusContribution(sizeBytes int64, publishedAt, now time.Time, seeders float64) float64 {
	weeks := now.Sub(publishedAt).Hours() / (24 * 7)
	ageFactor := -math.Expm1(-math.Ln10 * weeks / 4)
	rarityFactor := 1 + math.Sqrt2*math.Pow(10, -(seeders-1)/6)
	return ageFactor * (float64(sizeBytes) / (1 << 30)) * rarityFactor
}

func bonusPerHour(a float64, seeds int) float64 {
	return float64(min(seeds, 7)) + 100*2/math.Pi*math.Atan(a/300)
}

func validateBonusSnapshot(snapshot BonusSnapshot, now time.Time, maxAge time.Duration) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", port.ErrInvalidTrackerSnapshot, reason)
	}
	if now.IsZero() || snapshot.ObservedAt.IsZero() || maxAge <= 0 || now.Before(snapshot.ObservedAt) || now.Sub(snapshot.ObservedAt) >= maxAge {
		return invalid("bonus snapshot expired or timestamp invalid")
	}
	if math.IsNaN(snapshot.AccountA) || math.IsInf(snapshot.AccountA, 0) || snapshot.AccountA < 0 || snapshot.SeedCount < 0 {
		return invalid("bonus account values invalid")
	}
	seeded := 0
	seen := make(map[domain.TorrentID]bool)
	for hash, torrent := range snapshot.Torrents {
		if hash != canonicalHash(hash) || !planHashPattern.MatchString(string(hash)) || !validTorrentID(torrent.ID) || seen[torrent.ID] || torrent.SizeBytes <= 0 || torrent.Seeders < 0 || (torrent.Seeding && torrent.Seeders == 0) || torrent.PublishedAt.IsZero() || torrent.PublishedAt.After(snapshot.ObservedAt) {
			return invalid("bonus torrent metadata invalid")
		}
		seen[torrent.ID] = true
		if torrent.Seeding {
			seeded++
		}
	}
	if seeded != snapshot.SeedCount {
		return invalid("bonus seeding snapshot incomplete")
	}
	return nil
}

// BuildBonusPlanWithDiskSpace retains the existing safety plan and chooses at most
// one positive-net-benefit replacement from the current TopFree candidate set.
func BuildBonusPlanWithDiskSpace(
	maximum int64, space domain.DiskSpace, reserve int64,
	now time.Time, maxAge time.Duration,
	current, queued []domain.Candidate, managed []domain.ManagedTorrent,
	downloadBlocked bool, snapshot BonusSnapshot,
) (Plan, error) {
	if err := validateBonusSnapshot(snapshot, now, maxAge); err != nil {
		return Plan{}, err
	}
	// Reuse ownership, tag synchronization, normalization and forced-expiry cleanup.
	plan, err := BuildQueuedPlanWithDiskSpace(maximum, space, reserve, now, current, queued, managed, true)
	if err != nil {
		return Plan{}, err
	}
	disk := &diskCapacity{path: plan.EffectiveSavePath, freeBytes: space.FreeBytes, reserve: reserve}
	contributions := make(map[domain.TorrentHash]float64)
	beforeA := float64(0)
	hashes := make([]domain.TorrentHash, 0, len(snapshot.Torrents))
	for hash := range snapshot.Torrents {
		hashes = append(hashes, hash)
	}
	sortHashes(hashes)
	for _, hash := range hashes {
		torrent := snapshot.Torrents[hash]
		if torrent.Seeding {
			value := bonusContribution(torrent.SizeBytes, torrent.PublishedAt, now, float64(torrent.Seeders))
			contributions[hash] = value
			beforeA += value
		}
	}
	// Compute a consistent baseline from the full account seeding list, including
	// protected torrents. They affect the estimate but never management capacity.
	baseA, baseSeeds := beforeA, snapshot.SeedCount
	removedExpired := make(map[domain.TorrentHash]bool)
	for _, torrent := range plan.RemoveExpired {
		removedExpired[torrent.Hash] = true
		if value, seeded := contributions[torrent.Hash]; seeded {
			baseA -= value
			baseSeeds--
		}
	}
	// Floating point subtraction may leave a tiny negative residue at zero.
	baseA = math.Max(0, baseA)
	plan.Bonus = &BonusEstimate{
		ObservedAt: snapshot.ObservedAt, ValidUntil: snapshot.ObservedAt.Add(maxAge), ReportedA: snapshot.AccountA,
		BeforeA: beforeA, AfterA: baseA,
		BeforePerHour: bonusPerHour(beforeA, snapshot.SeedCount),
		AfterPerHour:  bonusPerHour(baseA, baseSeeds),
		RemovedA:      beforeA - baseA,
	}
	plan.Bonus.NetPerHour = plan.Bonus.AfterPerHour - plan.Bonus.BeforePerHour
	currentHashes := make(map[domain.TorrentHash]bool)
	for _, candidate := range current {
		currentHashes[canonicalHash(candidate.Hash)] = true
	}
	var stale []domain.ManagedTorrent
	for _, torrent := range managed {
		torrent.Hash = canonicalHash(torrent.Hash)
		if !tagsInManagementScope(torrent.Tags) || currentHashes[torrent.Hash] || removedExpired[torrent.Hash] || (!hasTag(torrent.Tags, ManagedTag) && !hasTag(torrent.Tags, DeleteTag)) {
			continue
		}
		if _, known := contributions[torrent.Hash]; !known {
			plan.BonusUnavailable = append(plan.BonusUnavailable, torrent.Hash)
			continue
		}
		stale = append(stale, torrent)
	}
	sortHashes(plan.BonusUnavailable)
	if downloadBlocked {
		return plan, nil
	}

	bestGain := float64(0)
	var best *DownloadPlan
	var bestFreed, bestPhysical int64
	var bestAddedA, bestRemovedA float64
	bestAfterA, bestAfterSeeds := baseA, baseSeeds
	scores := make(map[domain.TorrentHash]float64)
	for _, candidate := range plan.Queued {
		if !candidate.FreeUntil.After(now) {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: SkipFreePeriodExpired})
			continue
		}
		metadata, exists := snapshot.Torrents[candidate.Hash]
		if !exists || metadata.ID != candidate.ID {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: SkipBonusUnavailable})
			continue
		}
		// Already seeding through another client gives this account no new seed.
		if metadata.Seeding {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: SkipNoBonusGain})
			continue
		}
		cleanup := append([]domain.ManagedTorrent(nil), stale...)
		sortBonusCleanup(cleanup, contributions, baseA, baseSeeds,
			plan.ProjectedFreeSpaceBytes, candidate.SizeBytes, disk)
		selected, _, freed, physical, fits := selectCleanup(maximum, plan.ProjectedDiskUsageBytes,
			candidate.SizeBytes, plan.ProjectedFreeSpaceBytes, cleanup, disk)
		if !fits {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{
				Candidate: candidate, Reason: SkipInsufficientSpace,
				AvailableBytes: availableAfterAllCleanup(maximum, plan.ProjectedDiskUsageBytes, plan.ProjectedFreeSpaceBytes, stale, disk),
			})
			continue
		}
		// The candidate's observed swarm does not yet include our completed seed.
		addedA := bonusContribution(candidate.SizeBytes, metadata.PublishedAt, now, float64(metadata.Seeders)+1)
		removedA := float64(0)
		for _, torrent := range selected {
			removedA += contributions[torrent.Hash]
		}
		afterA := math.Max(0, baseA-removedA) + addedA
		afterSeeds := baseSeeds - len(selected) + 1
		gain := bonusPerHour(afterA, afterSeeds) - bonusPerHour(baseA, baseSeeds)
		scores[candidate.Hash] = gain
		if gain <= 0 {
			plan.Skipped = append(plan.Skipped, SkippedCandidate{Candidate: candidate, Reason: SkipNoBonusGain})
			continue
		}
		// Queued is ordered by free_time; stable ordering breaks equal gains without changing that priority.
		if best != nil && gain <= bestGain {
			continue
		}
		bestGain = gain
		best = &DownloadPlan{Candidate: candidate, DeleteBefore: selected}
		bestFreed, bestPhysical = freed, physical
		bestAddedA, bestRemovedA = addedA, removedA
		bestAfterA, bestAfterSeeds = afterA, afterSeeds
	}
	sort.SliceStable(plan.Queued, func(i, j int) bool {
		first, firstKnown := scores[plan.Queued[i].Hash]
		second, secondKnown := scores[plan.Queued[j].Hash]
		if firstKnown != secondKnown {
			return firstKnown
		}
		return first > second
	})
	if best == nil {
		return plan, nil
	}
	plan.Downloads = []DownloadPlan{*best}
	plan.ProjectedDiskUsageBytes += best.Candidate.SizeBytes - bestFreed
	plan.ProjectedFreeSpaceBytes = saturatingAdd(plan.ProjectedFreeSpaceBytes, bestPhysical) - best.Candidate.SizeBytes
	deleted := make(map[domain.TorrentHash]bool)
	for _, torrent := range best.DeleteBefore {
		deleted[torrent.Hash] = true
	}
	marks := make([]domain.TorrentHash, 0, len(plan.MarkForDeletion))
	for _, hash := range plan.MarkForDeletion {
		if !deleted[hash] {
			marks = append(marks, hash)
		}
	}
	plan.MarkForDeletion = marks
	plan.Bonus.AddedA = bestAddedA
	plan.Bonus.RemovedA += bestRemovedA
	plan.Bonus.AfterA = bestAfterA
	plan.Bonus.AfterPerHour = bonusPerHour(bestAfterA, bestAfterSeeds)
	plan.Bonus.NetPerHour = plan.Bonus.AfterPerHour - plan.Bonus.BeforePerHour
	plan.Bonus.ReplacementGainPerHour = bestGain
	return plan, nil
}

func sortBonusCleanup(torrents []domain.ManagedTorrent, values map[domain.TorrentHash]float64,
	a float64, seeds int, freeBytes, size int64, disk *diskCapacity,
) {
	physicalShortage := !hasReservedCapacity(freeBytes, disk.reserve, size)
	lossDensity := func(torrent domain.ManagedTorrent) float64 {
		bytes := torrent.SizeBytes
		if physicalShortage && physicallyReleasable(torrent, disk) > 0 {
			bytes = physicallyReleasable(torrent, disk)
		}
		if bytes == 0 {
			return math.Inf(1)
		}
		loss := bonusPerHour(a, seeds) - bonusPerHour(math.Max(0, a-values[torrent.Hash]), seeds-1)
		return loss / float64(bytes)
	}
	sort.Slice(torrents, func(i, j int) bool {
		first, second := torrents[i], torrents[j]
		if physicalShortage {
			firstReleases := physicallyReleasable(first, disk) > 0
			secondReleases := physicallyReleasable(second, disk) > 0
			if firstReleases != secondReleases {
				return firstReleases
			}
		}
		firstLoss, secondLoss := lossDensity(first), lossDensity(second)
		if firstLoss != secondLoss {
			return firstLoss < secondLoss
		}
		if !first.AddedAt.Equal(second.AddedAt) {
			return first.AddedAt.Before(second.AddedAt)
		}
		return first.Hash < second.Hash
	})
}
