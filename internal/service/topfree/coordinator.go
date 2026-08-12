package topfree

import (
	"context"
	"errors"
	"fmt"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

var (
	// ErrManagedSourceRequired indicates that no complete qBittorrent snapshot source was supplied.
	ErrManagedSourceRequired = errors.New("TopFree managed torrent source is required")
	// ErrPlanExecutorRequired indicates that no plan executor was supplied.
	ErrPlanExecutorRequired = errors.New("TopFree plan executor is required")
)

// ManagedTorrentSource supplies the complete qBittorrent snapshot used for safe planning.
type ManagedTorrentSource interface {
	ListAll(context.Context) ([]domain.ManagedTorrent, error)
}

// PlanExecutor applies one validated TopFree plan.
type PlanExecutor interface {
	Execute(context.Context, Plan) error
}

// Coordinator connects one TopFree snapshot to planning and explicit execution.
type Coordinator struct {
	managedSource     ManagedTorrentSource
	executor          PlanExecutor
	maxDiskUsageBytes int64
}

// NewCoordinator creates a single-cycle TopFree coordinator.
func NewCoordinator(
	managedSource ManagedTorrentSource,
	executor PlanExecutor,
	maxDiskUsageBytes int64,
) (*Coordinator, error) {
	if managedSource == nil {
		return nil, ErrManagedSourceRequired
	}
	if executor == nil {
		return nil, ErrPlanExecutorRequired
	}
	if maxDiskUsageBytes <= 0 {
		return nil, ErrInvalidDiskLimit
	}
	return &Coordinator{
		managedSource:     managedSource,
		executor:          executor,
		maxDiskUsageBytes: maxDiskUsageBytes,
	}, nil
}

// RunOnce reads the complete client snapshot, builds a plan, and executes it.
// If execution fails, the computed plan is returned with the error for diagnostics.
func (c *Coordinator) RunOnce(ctx context.Context, candidates []domain.Candidate) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	managed, err := c.managedSource.ListAll(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list all qBittorrent torrents: %w", err)
	}
	plan, err := BuildPlan(c.maxDiskUsageBytes, candidates, managed)
	if err != nil {
		return Plan{}, fmt.Errorf("build TopFree plan: %w", err)
	}
	if err := c.executor.Execute(ctx, plan); err != nil {
		return plan, fmt.Errorf("execute TopFree plan: %w", err)
	}
	return plan, nil
}

// Handle adapts RunOnce to Poller's Handler signature.
func (c *Coordinator) Handle(ctx context.Context, candidates []domain.Candidate) error {
	_, err := c.RunOnce(ctx, candidates)
	return err
}
