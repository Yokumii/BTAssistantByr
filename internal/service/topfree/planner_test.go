package topfree

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

var fixtureTime = time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

func TestBuildPlanSynchronizesTagsAndAddsNewCandidate(t *testing.T) {
	t.Parallel()

	candidates := []domain.Candidate{
		candidate(1, 100, 1, 1),
		candidate(2, 200, 1, 1),
		candidate(3, 100, 1, 1),
	}
	managed := []domain.ManagedTorrent{
		managedTorrent(1, 100, 1, ManagedTag, DeleteTag),
		managedTorrent(2, 200, 2),
		managedTorrent(4, 300, 3, ManagedTag),
		managedTorrent(5, 400, 4, DeleteTag),
	}

	plan, err := BuildPlan(1400, candidates, managed)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	assertHashes(t, plan.TagAsManaged, torrentHash(2))
	assertHashes(t, plan.UnmarkForDeletion, torrentHash(1))
	assertHashes(t, plan.MarkForDeletion, torrentHash(4))
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != torrentHash(3) || len(plan.Downloads[0].DeleteBefore) != 0 {
		t.Fatalf("Downloads = %+v", plan.Downloads)
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped = %+v", plan.Skipped)
	}
	if plan.CurrentDiskUsageBytes != 1000 || plan.ProjectedDiskUsageBytes != 1100 {
		t.Errorf("disk usage = (%d, %d), want (1000, 1100)", plan.CurrentDiskUsageBytes, plan.ProjectedDiskUsageBytes)
	}
}

func TestBuildPlanRollsBackUnnecessaryPrefixDeletion(t *testing.T) {
	t.Parallel()

	candidates := []domain.Candidate{
		candidate(1, 40, 1, 1),
		candidate(3, 40, 1, 1),
	}
	managed := []domain.ManagedTorrent{
		managedTorrent(1, 40, 3, ManagedTag),
		managedTorrent(4, 20, 1, ManagedTag),
		managedTorrent(5, 30, 2, ManagedTag),
	}

	plan, err := BuildPlan(100, candidates, managed)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Downloads) != 1 {
		t.Fatalf("len(Downloads) = %d, want 1", len(plan.Downloads))
	}
	deletions := plan.Downloads[0].DeleteBefore
	if len(deletions) != 1 || deletions[0].Hash != torrentHash(5) {
		t.Errorf("DeleteBefore = %+v", deletions)
	}
	assertHashes(t, plan.MarkForDeletion, torrentHash(4))
	if plan.CurrentDiskUsageBytes != 90 || plan.ProjectedDiskUsageBytes != 100 {
		t.Errorf("disk usage = (%d, %d), want (90, 100)", plan.CurrentDiskUsageBytes, plan.ProjectedDiskUsageBytes)
	}
}

func TestBuildPlanNeverDeletesCurrentTopFree(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlan(
		100,
		[]domain.Candidate{candidate(1, 90, 1, 1), candidate(2, 20, 1, 1)},
		[]domain.ManagedTorrent{managedTorrent(1, 90, 1, ManagedTag, DeleteTag)},
	)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	assertHashes(t, plan.UnmarkForDeletion, torrentHash(1))
	if len(plan.Downloads) != 0 {
		t.Fatalf("Downloads = %+v, want none", plan.Downloads)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Candidate.Hash != torrentHash(2) || plan.Skipped[0].AvailableBytes != 10 {
		t.Fatalf("Skipped = %+v", plan.Skipped)
	}
}

func TestBuildPlanTreatsDeleteTagAsExplicitDeletionAuthorization(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlan(
		100,
		[]domain.Candidate{candidate(1, 80, 1, 1), candidate(2, 20, 1, 1)},
		[]domain.ManagedTorrent{
			managedTorrent(1, 80, 1, ManagedTag),
			managedTorrent(9, 100, 2, DeleteTag),
		},
	)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != torrentHash(9) {
		t.Fatalf("toDel-only torrent was not reclaimed: %+v", plan)
	}
	if plan.CurrentDiskUsageBytes != 180 || plan.ProjectedDiskUsageBytes != 100 || len(plan.MarkForDeletion) != 0 {
		t.Errorf("unexpected authorized deletion plan: %+v", plan)
	}
}

func TestBuildPlanKeepsAuthorizedToDelTorrentWhenCapacityAlreadyFits(t *testing.T) {
	t.Parallel()

	authorized := managedTorrent(9, 90, 1, DeleteTag)
	plan, err := BuildPlan(200, []domain.Candidate{candidate(1, 20, 1, 1)}, []domain.ManagedTorrent{authorized})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 0 {
		t.Fatalf("authorized torrent was deleted without capacity pressure: %+v", plan)
	}
}

func TestBuildPlanMatchesLegacyReverseRollbackSelection(t *testing.T) {
	t.Parallel()

	stale := []domain.ManagedTorrent{
		managedTorrent(4, 1, 1, ManagedTag),
		managedTorrent(5, 2, 2, ManagedTag),
		managedTorrent(6, 7, 3, ManagedTag),
	}
	plan, err := BuildPlan(10, []domain.Candidate{candidate(1, 8, 1, 1)}, stale)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Downloads) != 1 {
		t.Fatalf("Downloads = %+v", plan.Downloads)
	}
	deletions := plan.Downloads[0].DeleteBefore
	if len(deletions) != 2 || deletions[0].Hash != torrentHash(4) || deletions[1].Hash != torrentHash(6) {
		t.Fatalf("DeleteBefore = %+v, want legacy rollback selection [1, 7]", deletions)
	}
}

func TestBuildPlanExcludesTorrentsWithManualTags(t *testing.T) {
	t.Parallel()

	existing := candidate(1, 90, 1, 1)
	next := candidate(2, 20, 1, 1)
	manualCurrent := managedTorrent(1, 90, 1, "keep")
	manualStale := managedTorrent(9, 100, 2, ManagedTag, "keep")
	plan, err := BuildPlan(100, []domain.Candidate{existing, next}, []domain.ManagedTorrent{manualCurrent, manualStale})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.TagAsManaged) != 0 || len(plan.MarkForDeletion) != 0 || len(plan.RemoveExpired) != 0 || plan.CurrentDiskUsageBytes != 0 {
		t.Fatalf("manual-tagged torrents entered plan: %+v", plan)
	}
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != next.Hash {
		t.Fatalf("Downloads = %+v, want only absent candidate", plan.Downloads)
	}
}

func TestBuildPlanOrdersCandidatesByPeerDemand(t *testing.T) {
	t.Parallel()

	candidates := []domain.Candidate{
		candidate(1, 10, 10, 10),
		candidate(2, 10, 2, 6),
		candidate(3, 10, 0, 100),
	}
	plan, err := BuildPlan(100, candidates, nil)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Downloads) != 3 {
		t.Fatalf("len(Downloads) = %d, want 3", len(plan.Downloads))
	}
	want := []domain.TorrentHash{torrentHash(2), torrentHash(1), torrentHash(3)}
	for index, download := range plan.Downloads {
		if download.Candidate.Hash != want[index] {
			t.Errorf("Downloads[%d] hash = %s, want %s", index, download.Candidate.Hash, want[index])
		}
	}
}

func TestBuildPlanDoesNotConsumeCleanupForSkippedCandidate(t *testing.T) {
	t.Parallel()

	large := candidate(1, 120, 1, 100)
	small := candidate(2, 80, 1, 1)
	stale := managedTorrent(9, 50, 1, ManagedTag)
	plan, err := BuildPlan(100, []domain.Candidate{small, large}, []domain.ManagedTorrent{stale})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Candidate.Hash != large.Hash || plan.Skipped[0].AvailableBytes != 100 {
		t.Fatalf("Skipped = %+v", plan.Skipped)
	}
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != small.Hash {
		t.Fatalf("Downloads = %+v", plan.Downloads)
	}
	if len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != stale.Hash {
		t.Fatalf("DeleteBefore = %+v", plan.Downloads[0].DeleteBefore)
	}
	if plan.ProjectedDiskUsageBytes != 80 {
		t.Errorf("ProjectedDiskUsageBytes = %d, want 80", plan.ProjectedDiskUsageBytes)
	}
}

func TestBuildPlanWithDiskSpacePlansPhysicalCleanup(t *testing.T) {
	t.Parallel()

	stale := managedTorrent(9, 30, 1, ManagedTag)
	stale.SavePath = "/downloads"
	plan, err := BuildPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 30},
		10,
		[]domain.Candidate{candidate(1, 40, 1, 1)},
		[]domain.ManagedTorrent{stale},
	)
	if err != nil {
		t.Fatalf("BuildPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != stale.Hash {
		t.Fatalf("Downloads = %+v", plan.Downloads)
	}
	if plan.EffectiveSavePath != "/downloads" || plan.InitialFreeSpaceBytes != 30 || plan.ProjectedFreeSpaceBytes != 20 || plan.MinFreeSpaceBytes != 10 {
		t.Errorf("disk space projection = %+v", plan)
	}
}

func TestBuildPlanWithDiskSpaceDoesNotCountOtherPathCleanup(t *testing.T) {
	t.Parallel()

	stale := managedTorrent(9, 100, 1, ManagedTag)
	stale.SavePath = "/other-disk"
	plan, err := BuildPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 30},
		10,
		[]domain.Candidate{candidate(1, 40, 1, 1)},
		[]domain.ManagedTorrent{stale},
	)
	if err != nil {
		t.Fatalf("BuildPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Downloads) != 0 || len(plan.Skipped) != 1 {
		t.Fatalf("unexpected physical capacity decision: %+v", plan)
	}
	if plan.Skipped[0].AvailableBytes != 20 || len(plan.MarkForDeletion) != 1 {
		t.Errorf("unexpected skipped decision: %+v", plan)
	}
}

func TestBuildPlanWithDiskSpaceCountsOnlyDownloadedBytesAsReleasable(t *testing.T) {
	t.Parallel()

	stale := managedTorrent(9, 100, 1, ManagedTag)
	stale.CompletedBytes = 10
	stale.SavePath = "/downloads"
	plan, err := BuildPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 30},
		10,
		[]domain.Candidate{candidate(1, 40, 1, 1)},
		[]domain.ManagedTorrent{stale},
	)
	if err != nil {
		t.Fatalf("BuildPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Downloads) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].AvailableBytes != 30 {
		t.Fatalf("partial cleanup was overestimated: %+v", plan)
	}
}

func TestBuildPlanReverseRollbackPreservesBothCapacityConstraints(t *testing.T) {
	t.Parallel()

	stale := []domain.ManagedTorrent{
		managedTorrent(4, 1, 1, ManagedTag),
		managedTorrent(5, 2, 2, ManagedTag),
		managedTorrent(6, 7, 3, ManagedTag),
	}
	for index := range stale {
		stale[index].SavePath = "/downloads"
	}
	plan, err := BuildPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 10},
		10,
		[]domain.Candidate{candidate(1, 8, 1, 1)},
		stale,
	)
	if err != nil {
		t.Fatalf("BuildPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Downloads) != 1 {
		t.Fatalf("Downloads = %+v", plan.Downloads)
	}
	deletions := plan.Downloads[0].DeleteBefore
	if len(deletions) != 2 || deletions[0].Hash != torrentHash(4) || deletions[1].Hash != torrentHash(6) {
		t.Fatalf("DeleteBefore = %+v, want physical-space rollback selection [1, 7]", deletions)
	}
	if plan.ProjectedFreeSpaceBytes != 10 {
		t.Fatalf("ProjectedFreeSpaceBytes = %d, want 10", plan.ProjectedFreeSpaceBytes)
	}
}

func TestBuildQueuedPlanReclaimsIncompleteToDelOnlyTorrentOnDemand(t *testing.T) {
	t.Parallel()

	authorized := managedTorrent(9, 90, 1, DeleteTag)
	authorized.CompletedBytes = 30
	authorized.AmountLeftBytes = 60
	authorized.State = "stoppedDL"
	authorized.SavePath = "/downloads"
	next := candidate(1, 20, 1, 1)
	plan, err := BuildQueuedPlanWithDiskSpace(
		100,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 100},
		10,
		fixtureTime,
		[]domain.Candidate{next},
		[]domain.Candidate{next},
		[]domain.ManagedTorrent{authorized},
		false,
	)
	if err != nil {
		t.Fatalf("BuildQueuedPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.RemoveExpired) != 0 {
		t.Fatalf("toDel-only torrent was treated as expired sc download: %+v", plan.RemoveExpired)
	}
	if len(plan.Downloads) != 1 || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != authorized.Hash {
		t.Fatalf("authorized cleanup plan = %+v", plan)
	}
}

func TestBuildPlanWithDiskSpaceValidatesCapacity(t *testing.T) {
	t.Parallel()

	validSpace := domain.DiskSpace{Path: "/downloads", FreeBytes: 100}
	if _, err := BuildPlanWithDiskSpace(100, domain.DiskSpace{}, 10, nil, nil); !errors.Is(err, ErrInvalidDiskSpace) {
		t.Errorf("invalid disk space error = %v", err)
	}
	if _, err := BuildPlanWithDiskSpace(100, validSpace, 0, nil, nil); !errors.Is(err, ErrInvalidFreeSpaceReserve) {
		t.Errorf("invalid reserve error = %v", err)
	}
}

func TestBuildQueuedPlanDownloadsOneBySizeToRemainingFreeTime(t *testing.T) {
	t.Parallel()

	longLarge := candidate(1, 100, 100, 1)
	longLarge.FreeUntil = fixtureTime.Add(20 * time.Hour) // score 5 bytes/hour
	shortSmall := candidate(2, 30, 1, 100)
	shortSmall.FreeUntil = fixtureTime.Add(5 * time.Hour) // score 6 bytes/hour
	plan, err := BuildQueuedPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 1000},
		10,
		fixtureTime,
		[]domain.Candidate{shortSmall, longLarge},
		[]domain.Candidate{shortSmall, longLarge},
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("BuildQueuedPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Queued) != 2 {
		t.Fatalf("Queued = %+v", plan.Queued)
	}
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != longLarge.Hash {
		t.Fatalf("Downloads = %+v, want lower size/remaining-time score", plan.Downloads)
	}
}

func TestBuildQueuedPlanWaitsForCurrentDownload(t *testing.T) {
	t.Parallel()

	current := candidate(1, 100, 1, 1)
	next := candidate(2, 20, 1, 1)
	downloading := managedTorrent(1, 100, 1, ManagedTag)
	downloading.CompletedBytes = 40
	downloading.AmountLeftBytes = 60
	downloading.State = "downloading"
	plan, err := BuildQueuedPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 1000},
		10,
		fixtureTime,
		[]domain.Candidate{current, next},
		[]domain.Candidate{next},
		[]domain.ManagedTorrent{downloading},
		true,
	)
	if err != nil {
		t.Fatalf("BuildQueuedPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.Downloads) != 0 || len(plan.Queued) != 1 || plan.Queued[0].Hash != next.Hash {
		t.Fatalf("unexpected blocked plan: %+v", plan)
	}
}

func TestBuildQueuedPlanRemovesIncompleteTorrentNoLongerTopFree(t *testing.T) {
	t.Parallel()

	stale := managedTorrent(9, 100, 1, ManagedTag)
	stale.CompletedBytes = 25
	stale.AmountLeftBytes = 75
	stale.State = "stoppedDL"
	stale.SavePath = "/downloads"
	plan, err := BuildQueuedPlanWithDiskSpace(
		1000,
		domain.DiskSpace{Path: "/downloads", FreeBytes: 100},
		10,
		fixtureTime,
		nil,
		nil,
		[]domain.ManagedTorrent{stale},
		false,
	)
	if err != nil {
		t.Fatalf("BuildQueuedPlanWithDiskSpace() error = %v", err)
	}
	if len(plan.RemoveExpired) != 1 || plan.RemoveExpired[0].Hash != stale.Hash {
		t.Fatalf("RemoveExpired = %+v", plan.RemoveExpired)
	}
	if len(plan.MarkForDeletion) != 0 || plan.ProjectedDiskUsageBytes != 0 || plan.ProjectedFreeSpaceBytes != 125 {
		t.Fatalf("unexpected expired cleanup plan: %+v", plan)
	}
}

func TestBuildPlanIsDeterministic(t *testing.T) {
	t.Parallel()

	candidates := []domain.Candidate{candidate(1, 40, 1, 1), candidate(2, 30, 2, 3), candidate(3, 20, 0, 4)}
	managed := []domain.ManagedTorrent{managedTorrent(8, 30, 2, ManagedTag), managedTorrent(9, 40, 1, ManagedTag)}
	first, err := BuildPlan(80, candidates, managed)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	second, err := BuildPlan(80, reverseCandidates(candidates), reverseManaged(managed))
	if err != nil {
		t.Fatalf("BuildPlan() reversed error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("plans differ by input order:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestBuildPlanLocalMultiRoundSimulation(t *testing.T) {
	t.Parallel()

	firstCandidate := candidate(1, 100, 1, 10)
	secondCandidate := candidate(2, 60, 1, 1)
	firstRound, err := BuildPlan(150, []domain.Candidate{secondCandidate, firstCandidate}, nil)
	if err != nil {
		t.Fatalf("first BuildPlan() error = %v", err)
	}
	if len(firstRound.Downloads) != 1 || firstRound.Downloads[0].Candidate.Hash != firstCandidate.Hash || len(firstRound.Skipped) != 1 || firstRound.Skipped[0].Candidate.Hash != secondCandidate.Hash {
		t.Fatalf("unexpected first round: %+v", firstRound)
	}

	simulatedClient := []domain.ManagedTorrent{
		managedTorrent(1, firstCandidate.SizeBytes, 1, ManagedTag),
	}
	secondRound, err := BuildPlan(150, []domain.Candidate{secondCandidate}, simulatedClient)
	if err != nil {
		t.Fatalf("second BuildPlan() error = %v", err)
	}
	if len(secondRound.Downloads) != 1 || secondRound.Downloads[0].Candidate.Hash != secondCandidate.Hash {
		t.Fatalf("unexpected second round download: %+v", secondRound.Downloads)
	}
	if len(secondRound.Downloads[0].DeleteBefore) != 1 || secondRound.Downloads[0].DeleteBefore[0].Hash != firstCandidate.Hash {
		t.Fatalf("unexpected second round cleanup: %+v", secondRound.Downloads[0].DeleteBefore)
	}
	if secondRound.ProjectedDiskUsageBytes != 60 {
		t.Errorf("second round projected usage = %d, want 60", secondRound.ProjectedDiskUsageBytes)
	}
}

func TestBuildPlanValidatesInput(t *testing.T) {
	t.Parallel()

	validCandidate := candidate(1, 10, 1, 1)
	validManaged := managedTorrent(8, 10, 1, ManagedTag)
	tests := []struct {
		name       string
		limit      int64
		candidates []domain.Candidate
		managed    []domain.ManagedTorrent
		wantErr    error
	}{
		{name: "invalid limit", candidates: []domain.Candidate{validCandidate}, wantErr: ErrInvalidDiskLimit},
		{name: "invalid candidate", limit: 100, candidates: []domain.Candidate{{}}, wantErr: ErrInvalidCandidate},
		{name: "duplicate candidate hash", limit: 100, candidates: []domain.Candidate{validCandidate, candidateWithID(validCandidate, 2)}, wantErr: ErrDuplicateCandidate},
		{name: "duplicate candidate ID", limit: 100, candidates: []domain.Candidate{validCandidate, candidateWithHash(validCandidate, torrentHash(2))}, wantErr: ErrDuplicateCandidate},
		{name: "invalid managed torrent", limit: 100, candidates: []domain.Candidate{validCandidate}, managed: []domain.ManagedTorrent{{}}, wantErr: ErrInvalidManagedTorrent},
		{name: "duplicate managed torrent", limit: 100, candidates: []domain.Candidate{validCandidate}, managed: []domain.ManagedTorrent{validManaged, validManaged}, wantErr: ErrDuplicateManagedTorrent},
		{name: "disk usage overflow", limit: 100, candidates: nil, managed: []domain.ManagedTorrent{managedTorrent(8, math.MaxInt64, 1, ManagedTag), managedTorrent(9, 1, 2, ManagedTag)}, wantErr: ErrDiskUsageOverflow},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildPlan(test.limit, test.candidates, test.managed)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("BuildPlan() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestBuildPlanDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	candidateValue := candidate(1, 10, 1, 1)
	candidateValue.Hash = domain.TorrentHash(strings.ToUpper(string(candidateValue.Hash)))
	managedValue := managedTorrent(8, 10, 1, " "+ManagedTag+" ")
	originalCandidate := candidateValue
	originalTags := append([]string(nil), managedValue.Tags...)
	if _, err := BuildPlan(100, []domain.Candidate{candidateValue}, []domain.ManagedTorrent{managedValue}); err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if candidateValue != originalCandidate || !reflect.DeepEqual(managedValue.Tags, originalTags) {
		t.Error("BuildPlan() mutated input")
	}
}

func candidate(number int, size int64, seeders, leechers int) domain.Candidate {
	return domain.Candidate{
		ID:        domain.TorrentID(strconv.Itoa(number)),
		Hash:      torrentHash(number),
		Name:      fmt.Sprintf("candidate-%d", number),
		SizeBytes: size,
		Seeders:   seeders,
		Leechers:  leechers,
		FreeUntil: fixtureTime.Add(24 * time.Hour),
	}
}

func managedTorrent(number int, size int64, addedHours int, tags ...string) domain.ManagedTorrent {
	return domain.ManagedTorrent{
		Hash:           torrentHash(number),
		Name:           fmt.Sprintf("managed-%d", number),
		SizeBytes:      size,
		CompletedBytes: size,
		AddedAt:        fixtureTime.Add(time.Duration(addedHours) * time.Hour),
		Tags:           append([]string(nil), tags...),
	}
}

func torrentHash(number int) domain.TorrentHash {
	return domain.TorrentHash(fmt.Sprintf("%040x", number))
}

func candidateWithID(candidate domain.Candidate, id int) domain.Candidate {
	candidate.ID = domain.TorrentID(strconv.Itoa(id))
	return candidate
}

func candidateWithHash(candidate domain.Candidate, hash domain.TorrentHash) domain.Candidate {
	candidate.Hash = hash
	return candidate
}

func reverseCandidates(values []domain.Candidate) []domain.Candidate {
	reversed := append([]domain.Candidate(nil), values...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func reverseManaged(values []domain.ManagedTorrent) []domain.ManagedTorrent {
	reversed := append([]domain.ManagedTorrent(nil), values...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func assertHashes(t *testing.T, got []domain.TorrentHash, want ...domain.TorrentHash) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hashes = %v, want %v", got, want)
	}
}
