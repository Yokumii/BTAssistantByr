package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
	FreeSpace(context.Context, string) (domain.DiskSpace, error)
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
	minFreeSpaceBytes int64
	savePath          string
	logger            *slog.Logger
}

// SetLogger enables structured planning logs. It must be called before RunOnce.
func (c *Coordinator) SetLogger(logger *slog.Logger) {
	c.logger = logger
}

// NewCoordinator creates a single-cycle TopFree coordinator.
func NewCoordinator(
	managedSource ManagedTorrentSource,
	executor PlanExecutor,
	maxDiskUsageBytes int64,
	minFreeSpaceBytes int64,
	savePath string,
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
	if minFreeSpaceBytes <= 0 {
		return nil, ErrInvalidFreeSpaceReserve
	}
	return &Coordinator{
		managedSource:     managedSource,
		executor:          executor,
		maxDiskUsageBytes: maxDiskUsageBytes,
		minFreeSpaceBytes: minFreeSpaceBytes,
		savePath:          strings.TrimSpace(savePath),
	}, nil
}

// RunOnce reads the complete client snapshot, builds a plan, and executes it.
// If execution fails, the computed plan is returned with the error for diagnostics.
func (c *Coordinator) RunOnce(ctx context.Context, candidates []domain.Candidate) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if c.logger != nil {
		c.logger.DebugContext(ctx, "checking qBittorrent disk space before planning", "configured_save_path", c.savePath)
	}
	diskSpace, err := c.managedSource.FreeSpace(ctx, c.savePath)
	if err != nil {
		return Plan{}, fmt.Errorf("check qBittorrent free space before planning: %w", err)
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "qBittorrent disk space checked before planning",
			"save_path", diskSpace.Path,
			"free_bytes", diskSpace.FreeBytes,
			"reserve_bytes", c.minFreeSpaceBytes,
		)
		c.logger.DebugContext(ctx, "loading complete qBittorrent snapshot")
	}
	managed, err := c.managedSource.ListAll(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("list all qBittorrent torrents: %w", err)
	}
	if c.logger != nil {
		c.logger.DebugContext(ctx, "qBittorrent snapshot loaded", "torrents", len(managed))
	}
	plan, err := BuildPlanWithDiskSpace(c.maxDiskUsageBytes, diskSpace, c.minFreeSpaceBytes, candidates, managed)
	if err != nil {
		return Plan{}, fmt.Errorf("build TopFree plan: %w", err)
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "TopFree plan built",
			"tag_as_managed", len(plan.TagAsManaged),
			"unmark_for_deletion", len(plan.UnmarkForDeletion),
			"mark_for_deletion", len(plan.MarkForDeletion),
			"downloads", len(plan.Downloads),
			"skipped", len(plan.Skipped),
			"current_disk_bytes", plan.CurrentDiskUsageBytes,
			"projected_disk_bytes", plan.ProjectedDiskUsageBytes,
			"initial_free_bytes", plan.InitialFreeSpaceBytes,
			"projected_free_bytes", plan.ProjectedFreeSpaceBytes,
			"reserve_bytes", plan.MinFreeSpaceBytes,
		)
		c.logger.DebugContext(ctx, "TopFree plan details",
			"tag_hashes", plan.TagAsManaged,
			"unmark_hashes", plan.UnmarkForDeletion,
			"mark_hashes", plan.MarkForDeletion,
			"downloads", plan.Downloads,
			"skipped", plan.Skipped,
		)
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
