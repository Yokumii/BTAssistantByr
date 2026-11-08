package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

var (
	// ErrManagedSourceRequired indicates that no complete qBittorrent snapshot source was supplied.
	ErrManagedSourceRequired = errors.New("TopFree managed torrent source is required")
	// ErrPlanExecutorRequired indicates that no plan executor was supplied.
	ErrPlanExecutorRequired = errors.New("TopFree plan executor is required")
	// ErrInvalidTorrentSizeLimit indicates that the optional per-torrent limit is negative.
	ErrInvalidTorrentSizeLimit = errors.New("TopFree maximum torrent size must not be negative")
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
	bonusSource         port.BonusTracker
	bonusInterval       time.Duration
	bonusSnapshot       *BonusSnapshot
	managedSource       ManagedTorrentSource
	executor            PlanExecutor
	maxDiskUsageBytes   int64
	maxTorrentSizeBytes int64
	minFreeSpaceBytes   int64
	savePath            string
	logger              *slog.Logger
	queue               downloadQueue
	now                 func() time.Time
}

// SetLogger sets the logger used by RunOnce; a nil logger disables logging.
func (c *Coordinator) SetLogger(logger *slog.Logger) {
	c.logger = logger
}

// NewCoordinator creates a single-cycle TopFree coordinator.
func NewCoordinator(
	managedSource ManagedTorrentSource,
	executor PlanExecutor,
	maxDiskUsageBytes int64,
	maxTorrentSizeBytes int64,
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
	if maxTorrentSizeBytes < 0 {
		return nil, ErrInvalidTorrentSizeLimit
	}
	if minFreeSpaceBytes <= 0 {
		return nil, ErrInvalidFreeSpaceReserve
	}
	return &Coordinator{
		managedSource:       managedSource,
		executor:            executor,
		maxDiskUsageBytes:   maxDiskUsageBytes,
		maxTorrentSizeBytes: maxTorrentSizeBytes,
		minFreeSpaceBytes:   minFreeSpaceBytes,
		savePath:            strings.TrimSpace(savePath),
		now:                 time.Now,
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
		c.logger.DebugContext(ctx, "qBittorrent disk space checked before planning",
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
	eligibleCandidates, oversizedCandidates, err := candidatesWithinSizeLimit(candidates, c.maxTorrentSizeBytes)
	if err != nil {
		return Plan{}, fmt.Errorf("apply TopFree torrent size limit: %w", err)
	}
	manageable := torrentsInManagementScope(managed)
	if c.logger != nil {
		if len(oversizedCandidates) > 0 {
			c.logger.DebugContext(ctx, "excluded oversized TopFree torrents from download queue",
				"excluded", len(oversizedCandidates),
				"max_torrent_size_bytes", c.maxTorrentSizeBytes,
			)
			if c.logger.Enabled(ctx, slog.LevelDebug) {
				for _, candidate := range oversizedCandidates {
					attrs := append(candidateLogAttrs(candidate), slog.Int64("max_torrent_size_bytes", c.maxTorrentSizeBytes))
					c.logger.LogAttrs(ctx, slog.LevelDebug, "TopFree candidate exceeds size limit", attrs...)
				}
			}
		}
		c.logger.DebugContext(ctx, "qBittorrent management scope applied",
			"in_scope", len(manageable),
			"out_of_scope", len(managed)-len(manageable),
		)
	}
	queue, err := c.queue.sync(candidates, eligibleCandidates, managed)
	if err != nil {
		return Plan{}, fmt.Errorf("reconcile TopFree download queue: %w", err)
	}
	if c.logger != nil && len(queue.removed) > 0 {
		c.logger.InfoContext(ctx, "removed torrents no longer TopFree from download queue", "hashes", queue.removed)
	}
	if c.logger != nil {
		c.logger.DebugContext(ctx, "TopFree queue reconciled",
			"queued", len(queue.candidates), "download_blocked", queue.downloadBlocked,
			"pending_hash", queue.pendingHash, "blocking_downloads", len(queue.blocking),
		)
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			if queue.pendingHash != "" {
				c.logger.DebugContext(ctx, "submitted TopFree torrent blocks next download", "reason", "awaiting_client_visibility", "hash", queue.pendingHash)
			}
			for _, torrent := range queue.blocking {
				attrs := append(managedTorrentLogAttrs(torrent), slog.String("reason", "unfinished_current_topfree"))
				c.logger.LogAttrs(ctx, slog.LevelDebug, "unfinished current TopFree torrent blocks next download", attrs...)
			}
		}
	}
	var plan Plan
	if c.bonusSource == nil {
		plan, err = BuildQueuedPlanWithDiskSpace(c.maxDiskUsageBytes, diskSpace, c.minFreeSpaceBytes,
			c.now(), candidates, queue.candidates, managed, queue.downloadBlocked)
		if err != nil {
			return Plan{}, fmt.Errorf("build TopFree plan: %w", err)
		}
	} else {
		bonus, err := c.loadBonusSnapshot(ctx, queue.candidates)
		if err != nil {
			return Plan{}, fmt.Errorf("load TopFree bonus snapshot: %w", err)
		}
		plan, err = BuildBonusPlanWithDiskSpace(c.maxDiskUsageBytes, diskSpace, c.minFreeSpaceBytes,
			c.now(), c.bonusInterval, candidates, queue.candidates, managed, queue.downloadBlocked, bonus)
		if err != nil {
			return Plan{}, fmt.Errorf("build TopFree bonus plan: %w", err)
		}
		if c.logger != nil {
			c.logger.DebugContext(ctx, "TopFree bonus estimate",
				"observed_at", plan.Bonus.ObservedAt,
				"valid_until", plan.Bonus.ValidUntil,
				"reported_a", plan.Bonus.ReportedA,
				"before_a", plan.Bonus.BeforeA,
				"after_a", plan.Bonus.AfterA,
				"before_per_hour", plan.Bonus.BeforePerHour,
				"after_per_hour", plan.Bonus.AfterPerHour,
				"added_a", plan.Bonus.AddedA,
				"removed_a", plan.Bonus.RemovedA,
				"net_per_hour", plan.Bonus.NetPerHour,
				"replacement_gain_per_hour", plan.Bonus.ReplacementGainPerHour,
			)
			if len(plan.BonusUnavailable) > 0 {
				c.logger.WarnContext(ctx, "excluded cleanup torrents with unknown bonus metadata", "hashes", plan.BonusUnavailable)
			}
		}
	}
	if c.logger != nil {
		c.logger.DebugContext(ctx, "TopFree plan built",
			"tag_as_managed", len(plan.TagAsManaged),
			"unmark_for_deletion", len(plan.UnmarkForDeletion),
			"mark_for_deletion", len(plan.MarkForDeletion),
			"remove_expired", len(plan.RemoveExpired),
			"queued", len(plan.Queued),
			"downloads", len(plan.Downloads),
			"skipped", len(plan.Skipped),
			"download_blocked", queue.downloadBlocked,
			"save_path", plan.EffectiveSavePath,
			"current_disk_bytes", plan.CurrentDiskUsageBytes,
			"projected_disk_bytes", plan.ProjectedDiskUsageBytes,
			"initial_free_bytes", plan.InitialFreeSpaceBytes,
			"projected_free_bytes", plan.ProjectedFreeSpaceBytes,
			"reserve_bytes", plan.MinFreeSpaceBytes,
		)
		logPlanDetails(ctx, c.logger, plan, queue.downloadBlocked)
	}
	if err := c.executor.Execute(ctx, plan); err != nil {
		c.bonusSnapshot = nil
		return plan, fmt.Errorf("execute TopFree plan: %w", err)
	}
	if len(plan.Downloads) == 1 {
		c.queue.markSubmitted(plan.Downloads[0].Candidate.Hash)
	}
	if planHasWrites(plan) {
		c.bonusSnapshot = nil
	}
	return plan, nil
}

// Handle adapts RunOnce to Poller's Handler signature.
func (c *Coordinator) Handle(ctx context.Context, candidates []domain.Candidate) error {
	_, err := c.RunOnce(ctx, candidates)
	return err
}
