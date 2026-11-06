package topfree

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const bonusGiB = int64(1 << 30)

func TestBonusFormulaMatchesPublishedRule(t *testing.T) {
	t.Parallel()
	if got := bonusPerHour(2090.1, 96); math.Abs(got-97.92434102194744) > 1e-9 {
		t.Fatalf("hourly bonus = %.12f", got)
	}
	if got := bonusPerHour(0, 3); got != 3 {
		t.Fatalf("base bonus = %v", got)
	}
	value := bonusContribution(bonusGiB, fixtureTime.Add(-12*7*24*time.Hour), fixtureTime, 1)
	if math.Abs(value-2.411799348810722) > 1e-12 {
		t.Fatalf("contribution = %.12f", value)
	}
	if bonusContribution(bonusGiB, fixtureTime, fixtureTime, 1) != 0 {
		t.Fatal("newly published torrent must have zero age contribution")
	}
}

func TestBonusPlanSelectsNetGainInsteadOfLegacyQueueHead(t *testing.T) {
	t.Parallel()
	first := candidate(1, 10*bonusGiB, 100, 1)
	second := candidate(2, 20*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{first, second}, nil)
	metadata := snapshot.Torrents[first.Hash]
	metadata.PublishedAt = fixtureTime.Add(-24 * time.Hour)
	snapshot.Torrents[first.Hash] = metadata
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{first, second}, nil, snapshot, false)
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != second.Hash || plan.Queued[0].Hash != second.Hash {
		t.Fatalf("downloads = %+v, queue = %+v", plan.Downloads, plan.Queued)
	}
	if plan.Bonus.NetPerHour <= 0 {
		t.Fatalf("estimate = %+v", plan.Bonus)
	}
	wantAdded := bonusContribution(second.SizeBytes, snapshot.Torrents[second.Hash].PublishedAt, fixtureTime, 2)
	if math.Abs(plan.Bonus.AddedA-wantAdded) > 1e-12 {
		t.Fatal("predicted swarm must include our new seed")
	}
}

func TestBonusCleanupPreservesHigherValueOldSeed(t *testing.T) {
	t.Parallel()
	low := managedTorrent(1, 40*bonusGiB, 10, ManagedTag)
	high := managedTorrent(2, 40*bonusGiB, 1, ManagedTag)
	low.SavePath, high.SavePath = "/downloads", "/downloads"
	newTorrent := candidate(3, 40*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, []domain.ManagedTorrent{low, high})
	metadata := snapshot.Torrents[low.Hash]
	metadata.PublishedAt = fixtureTime.Add(-7 * 24 * time.Hour)
	metadata.Seeders = 100
	snapshot.Torrents[low.Hash] = metadata
	metadata = snapshot.Torrents[high.Hash]
	metadata.Seeders = 1
	snapshot.Torrents[high.Hash] = metadata
	plan := buildBonusFixture(t, 80*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{high, low}, snapshot, false)
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != low.Hash {
		t.Fatalf("downloads = %+v", plan.Downloads)
	}
	for _, hash := range plan.MarkForDeletion {
		if hash == low.Hash {
			t.Fatal("selected deletion must not also be tagged")
		}
	}
	if plan.ProjectedDiskUsageBytes > 80*bonusGiB || plan.ProjectedFreeSpaceBytes < bonusGiB {
		t.Fatalf("capacity constraints violated: %+v", plan)
	}
	if _, err := prepareExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
}

func TestBonusPlanRejectsNegativeReplacement(t *testing.T) {
	t.Parallel()
	old := managedTorrent(1, 80*bonusGiB, 1, ManagedTag)
	newTorrent := candidate(2, 20*bonusGiB, 100, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, []domain.ManagedTorrent{old})
	metadata := snapshot.Torrents[old.Hash]
	metadata.Seeders = 1
	snapshot.Torrents[old.Hash] = metadata
	plan := buildBonusFixture(t, 80*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{old}, snapshot, false)
	if len(plan.Downloads) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipNoBonusGain {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Bonus.NetPerHour != 0 || plan.ProjectedDiskUsageBytes != old.SizeBytes {
		t.Fatalf("negative replacement consumed cleanup: %+v", plan)
	}
}

func TestBonusPlanProtectsUnknownAndManuallyTaggedTorrents(t *testing.T) {
	t.Parallel()
	unknown := managedTorrent(1, 80*bonusGiB, 1, DeleteTag)
	protected := managedTorrent(2, 80*bonusGiB, 1, ManagedTag, "manual")
	newTorrent := candidate(3, 20*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, []domain.ManagedTorrent{protected})
	plan := buildBonusFixture(t, 80*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{unknown, protected}, snapshot, false)
	if len(plan.Downloads) != 0 || len(plan.BonusUnavailable) != 1 || plan.BonusUnavailable[0] != unknown.Hash {
		t.Fatalf("unexpected cleanup protection: %+v", plan)
	}
	if plan.CurrentDiskUsageBytes != unknown.SizeBytes || plan.Bonus.BeforeA <= 0 {
		t.Fatal("protected torrents should affect bonus estimates only")
	}
}

func TestBonusPlanRetainsSerialAndForcedExpiryRules(t *testing.T) {
	t.Parallel()
	expired := managedTorrent(1, 40*bonusGiB, 1, ManagedTag)
	expired.CompletedBytes, expired.AmountLeftBytes = 10*bonusGiB, 30*bonusGiB
	newTorrent := candidate(2, 20*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, nil)
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{expired}, snapshot, true)
	if len(plan.RemoveExpired) != 1 || len(plan.Downloads) != 0 {
		t.Fatalf("safety rules changed: %+v", plan)
	}
}

func TestBonusPlanAccountsForLosingBaseSeedPoints(t *testing.T) {
	t.Parallel()
	first := managedTorrent(1, bonusGiB, 1, ManagedTag)
	second := managedTorrent(2, bonusGiB, 2, ManagedTag)
	newTorrent := candidate(3, 2*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, []domain.ManagedTorrent{first, second})
	plan := buildBonusFixture(t, 2*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{first, second}, snapshot, false)
	if len(plan.Downloads) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipNoBonusGain {
		t.Fatalf("two seeds replaced with one lost a base point: %+v", plan)
	}
}

func TestBonusPlanReclaimsOnlySpaceOnTheTargetPath(t *testing.T) {
	t.Parallel()
	other := managedTorrent(1, 20*bonusGiB, 1, ManagedTag)
	other.SavePath = "/other"
	same := managedTorrent(2, 20*bonusGiB, 2, DeleteTag)
	same.SavePath = "/downloads"
	newTorrent := candidate(3, 20*bonusGiB, 1, 1)
	snapshot := bonusFixture([]domain.Candidate{newTorrent}, []domain.ManagedTorrent{other, same})
	plan := buildBonusFixture(t, 100*bonusGiB, 5*bonusGiB, []domain.Candidate{newTorrent}, []domain.ManagedTorrent{other, same}, snapshot, false)
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != same.Hash {
		t.Fatalf("physical cleanup = %+v", plan.Downloads)
	}
	if plan.ProjectedFreeSpaceBytes != 5*bonusGiB {
		t.Fatalf("projected free = %d", plan.ProjectedFreeSpaceBytes)
	}
}

func TestBonusPlanNeverReclaimsCurrentTopFree(t *testing.T) {
	t.Parallel()
	current := candidate(1, 80*bonusGiB, 100, 1)
	newTorrent := candidate(2, 40*bonusGiB, 1, 1)
	protected := managedTorrent(1, current.SizeBytes, 1, ManagedTag, DeleteTag)
	snapshot := bonusFixture([]domain.Candidate{current, newTorrent}, []domain.ManagedTorrent{protected})
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{current, newTorrent}, []domain.ManagedTorrent{protected}, snapshot, false)
	if len(plan.Downloads) != 0 || len(plan.RemoveExpired) != 0 || len(plan.UnmarkForDeletion) != 1 {
		t.Fatalf("current TopFree was not protected: %+v", plan)
	}
}

func TestBonusPlanDoesNotDuplicateAccountSeedingElsewhere(t *testing.T) {
	t.Parallel()
	current := candidate(1, bonusGiB, 1, 1)
	elsewhere := managedTorrent(1, bonusGiB, 1)
	snapshot := bonusFixture([]domain.Candidate{current}, []domain.ManagedTorrent{elsewhere})
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{current}, nil, snapshot, false)
	if len(plan.Downloads) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipNoBonusGain {
		t.Fatalf("duplicate account seeding = %+v", plan)
	}
}

func TestBonusPlanRejectsStaleOrIncompleteSnapshots(t *testing.T) {
	t.Parallel()
	newTorrent := candidate(1, bonusGiB, 1, 1)
	for _, change := range []func(*BonusSnapshot){
		func(s *BonusSnapshot) { s.ObservedAt = fixtureTime.Add(-time.Minute) },
		func(s *BonusSnapshot) { s.SeedCount = 1 },
		func(s *BonusSnapshot) { s.AccountA = math.NaN() },
		func(s *BonusSnapshot) {
			m := s.Torrents[newTorrent.Hash]
			m.PublishedAt = fixtureTime.Add(time.Hour)
			s.Torrents[newTorrent.Hash] = m
		},
	} {
		snapshot := bonusFixture([]domain.Candidate{newTorrent}, nil)
		change(&snapshot)
		_, err := BuildBonusPlanWithDiskSpace(100*bonusGiB, domain.DiskSpace{Path: "/downloads", FreeBytes: 100 * bonusGiB}, bonusGiB,
			fixtureTime, time.Minute, []domain.Candidate{newTorrent}, []domain.Candidate{newTorrent}, nil, false, snapshot)
		if !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestBonusApprovalIncludesEstimateAndValidity(t *testing.T) {
	t.Parallel()
	newTorrent := candidate(1, bonusGiB, 1, 1)
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{newTorrent}, nil, bonusFixture([]domain.Candidate{newTorrent}, nil), false)
	summary := approvalSummary(plan)
	for _, expected := range []string{"魔力计划有效至", "新增 A", "删除损失 A", "预计魔力/小时", "APPLY"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("approval missing %q", expected)
		}
	}
}

func bonusFixture(candidates []domain.Candidate, seeded []domain.ManagedTorrent) BonusSnapshot {
	snapshot := BonusSnapshot{ObservedAt: fixtureTime, SeedCount: len(seeded), Torrents: make(map[domain.TorrentHash]BonusTorrent)}
	for _, candidate := range candidates {
		snapshot.Torrents[candidate.Hash] = BonusTorrent{ID: candidate.ID, SizeBytes: candidate.SizeBytes,
			PublishedAt: fixtureTime.Add(-12 * 7 * 24 * time.Hour), Seeders: candidate.Seeders}
	}
	for index, torrent := range seeded {
		metadata, exists := snapshot.Torrents[torrent.Hash]
		if !exists {
			metadata.ID = domain.TorrentID(fmt.Sprint(index + 100))
		}
		metadata.SizeBytes, metadata.Seeders, metadata.Seeding = torrent.SizeBytes, 100, true
		metadata.PublishedAt = fixtureTime.Add(-12 * 7 * 24 * time.Hour)
		snapshot.Torrents[torrent.Hash] = metadata
	}
	return snapshot
}

func buildBonusFixture(t *testing.T, maximum, free int64, candidates []domain.Candidate, managed []domain.ManagedTorrent, snapshot BonusSnapshot, blocked bool) Plan {
	t.Helper()
	plan, err := BuildBonusPlanWithDiskSpace(maximum, domain.DiskSpace{Path: "/downloads", FreeBytes: free}, bonusGiB,
		fixtureTime, time.Minute, candidates, candidates, managed, blocked, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
