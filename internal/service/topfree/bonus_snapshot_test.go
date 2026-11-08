package topfree

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

func TestCoordinatorBonusRefreshFailureInvalidatesSnapshotBeforeWrites(t *testing.T) {
	t.Parallel()
	source := &fakeBonusTracker{}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(&fakeManagedSource{}, executor, 100*bonusGiB, 0, bonusGiB, "/downloads")
	if err != nil {
		t.Fatal(err)
	}
	now := fixtureTime
	coordinator.now = func() time.Time { return now }
	if err := coordinator.EnableBonusPriority(source, time.Minute); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := coordinator.RunOnce(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if source.accountCalls != 1 || executor.calls != 2 {
		t.Fatalf("account reads = %d, executions = %d", source.accountCalls, executor.calls)
	}
	now = now.Add(time.Minute)
	source.err = port.ErrInvalidTrackerSnapshot
	if _, err := coordinator.RunOnce(context.Background(), nil); !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
		t.Fatalf("error = %v", err)
	}
	if coordinator.bonusSnapshot != nil || executor.calls != 2 {
		t.Fatal("failed refresh used stale data or continued to writes")
	}
	source.err = nil
	if _, err := coordinator.RunOnce(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if source.accountCalls != 3 || executor.calls != 3 {
		t.Fatal("snapshot did not recover on the next cycle")
	}
}

func TestCoordinatorRebuildsSeedHashAssociationAndInvalidatesAfterMutation(t *testing.T) {
	t.Parallel()
	current := candidate(1, 10*bonusGiB, 1, 1)
	old := managedTorrent(2, 20*bonusGiB, 1, ManagedTag)
	source := &fakeBonusTracker{seeded: []domain.SeededTorrent{{ID: "2", SizeBytes: old.SizeBytes, Seeders: 100}}}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(&fakeManagedSource{torrents: []domain.ManagedTorrent{old}}, executor, 100*bonusGiB, 0, bonusGiB, "/downloads")
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return fixtureTime }
	if err := coordinator.EnableBonusPriority(source, time.Minute); err != nil {
		t.Fatal(err)
	}
	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{current})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Downloads) != 1 || len(plan.BonusUnavailable) != 0 || plan.Bonus.BeforeA <= 0 {
		t.Fatalf("old seed not reconstructed: %+v", plan)
	}
	if coordinator.bonusSnapshot != nil || source.metadataCalls != 2 {
		t.Fatal("mutation did not invalidate the bonus baseline")
	}
}

func TestBonusExecutorRejectsExpiredApprovalBeforeExternalCalls(t *testing.T) {
	t.Parallel()
	current := candidate(1, bonusGiB, 1, 1)
	plan := buildBonusFixture(t, 100*bonusGiB, 100*bonusGiB, []domain.Candidate{current}, nil, bonusFixture([]domain.Candidate{current}, nil), false)
	mutations := &fakeMutationClient{}
	urls := &fakeDownloadSource{}
	executor, err := NewExecutor(urls, mutations, "/downloads", bonusGiB)
	if err != nil {
		t.Fatal(err)
	}
	executor.now = func() time.Time { return fixtureTime.Add(time.Minute) }
	if err := executor.Execute(context.Background(), plan); !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
		t.Fatalf("error = %v", err)
	}
	if len(mutations.calls) != 0 || mutations.freeSpaceCalls != 0 || len(urls.calls) != 0 {
		t.Fatal("expired approval performed writes")
	}
}

func TestBonusExecutorStopsAfterCleanupIfEstimateExpires(t *testing.T) {
	t.Parallel()
	old := managedTorrent(2, 40*bonusGiB, 1, ManagedTag)
	old.SavePath = "/downloads"
	current := candidate(1, 40*bonusGiB, 1, 1)
	plan := Plan{
		EffectiveSavePath: "/downloads", MinFreeSpaceBytes: bonusGiB,
		Bonus:     &BonusEstimate{ObservedAt: fixtureTime, ValidUntil: fixtureTime.Add(time.Minute)},
		Downloads: []DownloadPlan{{Candidate: current, DeleteBefore: []domain.ManagedTorrent{old}}},
	}
	now := fixtureTime
	mutations := &fakeMutationClient{
		freeSpaceSequence: []int64{30 * bonusGiB, 60 * bonusGiB},
		onFreeSpace: func(call int) {
			if call == 2 {
				now = now.Add(time.Minute)
			}
		},
	}
	executor, err := NewExecutor(&fakeDownloadSource{}, mutations, "/downloads", bonusGiB)
	if err != nil {
		t.Fatal(err)
	}
	executor.now = func() time.Time { return now }
	if err := executor.Execute(context.Background(), plan); !errors.Is(err, port.ErrInvalidTrackerSnapshot) {
		t.Fatalf("error = %v", err)
	}
	if len(mutations.calls) != 3 || mutations.calls[0].name != "reannounce" || mutations.calls[1].name != "pause" || mutations.calls[2].name != "delete" {
		t.Fatalf("unexpected partial execution: %+v", mutations.calls)
	}
}

type fakeBonusTracker struct {
	seeded        []domain.SeededTorrent
	err           error
	accountCalls  int
	metadataCalls int
}

func (s *fakeBonusTracker) BonusAccount(context.Context) (domain.BonusAccount, error) {
	s.accountCalls++
	return domain.BonusAccount{UserID: "42", A: 1}, s.err
}

func (s *fakeBonusTracker) ListSeeding(context.Context, string) ([]domain.SeededTorrent, error) {
	return s.seeded, s.err
}

func (s *fakeBonusTracker) TorrentMetadata(_ context.Context, id domain.TorrentID) (domain.TorrentMetadata, error) {
	s.metadataCalls++
	hash := torrentHash(1)
	if id == "2" {
		hash = torrentHash(2)
	}
	return domain.TorrentMetadata{Hash: hash, PublishedAt: fixtureTime.Add(-12 * 7 * 24 * time.Hour)}, s.err
}
