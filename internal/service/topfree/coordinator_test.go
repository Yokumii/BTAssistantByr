package topfree

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestCoordinatorRunsCompleteLocalCycle(t *testing.T) {
	t.Parallel()

	currentUntagged := managedTorrent(1, 10, 2)
	staleManaged := managedTorrent(3, 90, 1, ManagedTag)
	managedSource := &fakeManagedSource{torrents: []domain.ManagedTorrent{currentUntagged, staleManaged}}
	downloadSource := &fakeDownloadSource{}
	mutationClient := &fakeMutationClient{}
	executor, err := NewExecutor(downloadSource, mutationClient, "/downloads")
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	coordinator, err := NewCoordinator(managedSource, executor, 100)
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
	executor, err := NewExecutor(&fakeDownloadSource{}, &fakeMutationClient{failOperation: "addTags", err: wantErr}, "")
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	coordinator, err := NewCoordinator(managedSource, executor, 100)
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	plan, err := coordinator.RunOnce(context.Background(), []domain.Candidate{candidate(1, 10, 1, 1)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunOnce() error = %v, want mutation error", err)
	}
	assertHashes(t, plan.TagAsManaged, torrentHash(1))
}

func TestCoordinatorStopsWhenSnapshotFails(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("read failed")
	managedSource := &fakeManagedSource{err: wantErr}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(managedSource, executor, 100)
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

func TestCoordinatorHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	managedSource := &fakeManagedSource{}
	executor := &fakePlanExecutor{}
	coordinator, err := NewCoordinator(managedSource, executor, 100)
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

	if _, err := NewCoordinator(nil, &fakePlanExecutor{}, 100); !errors.Is(err, ErrManagedSourceRequired) {
		t.Errorf("nil managed source error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, nil, 100); !errors.Is(err, ErrPlanExecutorRequired) {
		t.Errorf("nil executor error = %v", err)
	}
	if _, err := NewCoordinator(&fakeManagedSource{}, &fakePlanExecutor{}, 0); !errors.Is(err, ErrInvalidDiskLimit) {
		t.Errorf("invalid limit error = %v", err)
	}
}

type fakeManagedSource struct {
	torrents []domain.ManagedTorrent
	err      error
	calls    int
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
}

func (executor *fakePlanExecutor) Execute(context.Context, Plan) error {
	executor.calls++
	return executor.err
}
