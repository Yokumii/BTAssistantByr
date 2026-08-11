package topfree

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestCoordinatorRunsCompleteLocalCycle(t *testing.T) {
	t.Parallel()

	currentUntagged := managedTorrent(1, 10, 2)
	staleManaged := managedTorrent(3, 90, 1, ManagedTag)
	managedSource := &fakeManagedSource{torrents: []domain.ManagedTorrent{currentUntagged, staleManaged}}
	downloadSource := &fakeDownloadSource{}
	mutationClient := &fakeMutationClient{}
	executor, err := NewExecutor(downloadSource, mutationClient, "/downloads", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	coordinator, err := NewCoordinator(managedSource, executor, 100, 0, 1, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}

	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{
		candidate(1, 10, 1, 1),
		candidate(2, 20, 1, 2),
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if managedSource.calls != 1 {
		t.Errorf("ListAll() calls = %d, want 1", managedSource.calls)
	}
	assertHashes(t, plan.TagAsManaged, torrentHash(1))
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != torrentHash(2) || len(plan.Downloads[0].DeleteBefore) != 1 || plan.Downloads[0].DeleteBefore[0].Hash != torrentHash(3) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	wantOperations := []string{"addTags", "reannounce", "pause", "delete", "add"}
	operations := make([]string, len(mutationClient.calls))
	for index, call := range mutationClient.calls {
		operations[index] = call.name
	}
	if !reflect.DeepEqual(operations, wantOperations) {
		t.Errorf("operations = %v, want %v", operations, wantOperations)
	}
}

func TestCoordinatorReturnsPlanWhenExecutionFails(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("write failed")
	managedSource := &fakeManagedSource{torrents: []domain.ManagedTorrent{managedTorrent(1, 10, 1)}}
	executor, err := NewExecutor(&fakeDownloadSource{}, &fakeMutationClient{failOperation: "addTags", err: wantErr}, "", 1)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	coordinator, err := NewCoordinator(managedSource, executor, 100, 0, 1, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{candidate(1, 10, 1, 1)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunOnce() error = %v, want mutation error", err)
	}
	assertHashes(t, plan.TagAsManaged, torrentHash(1))
}

func TestCoordinatorAdvancesQueueOnlyAfterCurrentDownloadCompletes(t *testing.T) {
	t.Parallel()

	first := candidate(1, 100, 1, 1)
	first.FreeUntil = fixtureTime.Add(20 * time.Hour)
	second := candidate(2, 30, 1, 1)
	second.FreeUntil = fixtureTime.Add(5 * time.Hour)
	source := &fakeManagedSource{}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(source, executor, 1000, 0, 10, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	coordinator.now = func() time.Time { return fixtureTime }
	candidates := []domain.Candidate{second, first}

	firstPlan, err := coordinator.RunOnce(context.Background(), candidates)
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if len(firstPlan.Downloads) != 1 || firstPlan.Downloads[0].Candidate.Hash != first.Hash {
		t.Fatalf("first downloads = %+v", firstPlan.Downloads)
	}

	pendingPlan, err := coordinator.RunOnce(context.Background(), candidates)
	if err != nil {
		t.Fatalf("pending RunOnce() error = %v", err)
	}
	if len(pendingPlan.Downloads) != 0 {
		t.Fatalf("pending downloads = %+v", pendingPlan.Downloads)
	}

	active := managedTorrent(1, first.SizeBytes, 1, ManagedTag)
	active.CompletedBytes = 40
	active.AmountLeftBytes = 60
	active.State = "downloading"
	source.torrents = []domain.ManagedTorrent{active}
	activePlan, err := coordinator.RunOnce(context.Background(), candidates)
	if err != nil {
		t.Fatalf("active RunOnce() error = %v", err)
	}
	if len(activePlan.Downloads) != 0 {
		t.Fatalf("active downloads = %+v", activePlan.Downloads)
	}

	active.CompletedBytes = active.SizeBytes
	active.AmountLeftBytes = 0
	active.State = "stalledUP"
	source.torrents = []domain.ManagedTorrent{active}
	nextPlan, err := coordinator.RunOnce(context.Background(), candidates)
	if err != nil {
		t.Fatalf("completed RunOnce() error = %v", err)
	}
	if len(nextPlan.Downloads) != 1 || nextPlan.Downloads[0].Candidate.Hash != second.Hash {
		t.Fatalf("next downloads = %+v", nextPlan.Downloads)
	}
}

func TestCoordinatorExcludesTorrentsAboveOptionalSizeLimit(t *testing.T) {
	t.Parallel()

	large := candidate(1, 51, 1, 1)
	large.FreeUntil = fixtureTime.Add(100 * time.Hour)
	small := candidate(2, 50, 1, 1)
	small.FreeUntil = fixtureTime.Add(time.Hour)
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(&fakeManagedSource{}, executor, 1000, 50, 10, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	coordinator.now = func() time.Time { return fixtureTime }
	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{large, small})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(plan.Queued) != 1 || plan.Queued[0].Hash != small.Hash || len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != small.Hash {
		t.Fatalf("size-limited plan = %+v", plan)
	}
}

func TestCoordinatorLeavesManuallyTaggedTorrentsOutsideManagement(t *testing.T) {
	t.Parallel()

	existing := candidate(1, 40, 1, 1)
	next := candidate(2, 20, 1, 1)
	existingManual := managedTorrent(1, 40, 1, "keep")
	existingManual.CompletedBytes = 10
	existingManual.AmountLeftBytes = 30
	existingManual.State = "downloading"
	staleManual := managedTorrent(3, 80, 2, ManagedTag, "keep")
	staleManual.CompletedBytes = 20
	staleManual.AmountLeftBytes = 60
	staleManual.State = "stoppedDL"
	source := &fakeManagedSource{torrents: []domain.ManagedTorrent{existingManual, staleManual}}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(source, executor, 1000, 0, 10, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	coordinator.now = func() time.Time { return fixtureTime }
	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{existing, next})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(plan.TagAsManaged) != 0 || len(plan.RemoveExpired) != 0 || len(plan.MarkForDeletion) != 0 || plan.CurrentDiskUsageBytes != 0 {
		t.Fatalf("manual-tagged torrents entered management plan: %+v", plan)
	}
	if len(plan.Downloads) != 1 || plan.Downloads[0].Candidate.Hash != next.Hash {
		t.Fatalf("manual-tagged torrent blocked queue: %+v", plan.Downloads)
	}
}

func TestCoordinatorStopsWhenSnapshotFails(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("read failed")
	managedSource := &fakeManagedSource{err: wantErr}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(managedSource, executor, 100, 0, 1, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	_, err = coordinator.RunOnce(context.Background(), []domain.Candidate{candidate(1, 10, 1, 1)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunOnce() error = %v, want source error", err)
	}
	if executor.calls != 0 {
		t.Error("executor was called after snapshot failure")
	}
}

func TestCoordinatorStopsBeforePlanningWhenDiskCheckFails(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("disk check failed")
	managedSource := &fakeManagedSource{freeSpaceErr: wantErr}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(managedSource, executor, 100, 0, 1, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	_, err = coordinator.RunOnce(context.Background(), []domain.Candidate{candidate(1, 10, 1, 1)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunOnce() error = %v, want disk error", err)
	}
	if managedSource.calls != 0 || executor.calls != 0 {
		t.Error("coordinator continued after disk check failure")
	}
}

func TestCoordinatorHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	managedSource := &fakeManagedSource{}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(managedSource, executor, 100, 0, 1, "/downloads")
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = coordinator.RunOnce(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce() error = %v, want context.Canceled", err)
	}
	if managedSource.calls != 0 || executor.calls != 0 {
		t.Error("canceled coordinator reached dependencies")
	}
}

func TestNewCoordinatorValidatesDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewCoordinator(nil, &fakePlanExecutor{}, 100, 0, 1, ""); !errors.Is(err, ErrManagedSourceRequired) {
		t.Errorf("nil managed source error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, nil, 100, 0, 1, ""); !errors.Is(err, ErrPlanExecutorRequired) {
		t.Errorf("nil executor error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, &fakePlanExecutor{}, 0, 0, 1, ""); !errors.Is(err, ErrInvalidDiskLimit) {
		t.Errorf("invalid limit error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, &fakePlanExecutor{}, 100, -1, 1, ""); !errors.Is(err, ErrInvalidTorrentSizeLimit) {
		t.Errorf("invalid torrent size limit error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, &fakePlanExecutor{}, 100, 0, 0, ""); !errors.Is(err, ErrInvalidFreeSpaceReserve) {
		t.Errorf("invalid reserve error = %v", err)
	}
}

type fakeManagedSource struct {
	torrents       []domain.ManagedTorrent
	err            error
	calls          int
	freeSpaceErr   error
	freeSpaceCalls int
}

func (source *fakeManagedSource) FreeSpace(ctx context.Context, configuredPath string) (domain.DiskSpace, error) {
	if err := ctx.Err(); err != nil {
		return domain.DiskSpace{}, err
	}
	source.freeSpaceCalls++
	if source.freeSpaceErr != nil {
		return domain.DiskSpace{}, source.freeSpaceErr
	}
	if configuredPath == "" {
		configuredPath = "/downloads"
	}
	return domain.DiskSpace{Path: configuredPath, FreeBytes: 1 << 60}, nil
}

func (source *fakeManagedSource) ListAll(context.Context) ([]domain.ManagedTorrent, error) {
	source.calls++
	if source.err != nil {
		return nil, source.err
	}
	return append([]domain.ManagedTorrent(nil), source.torrents...), nil
}

type fakePlanExecutor struct {
	calls int
	err   error
	plans []Plan
}

func (executor *fakePlanExecutor) Execute(_ context.Context, plan Plan) error {
	executor.calls++
	executor.plans = append(executor.plans, plan)
	return executor.err
}
