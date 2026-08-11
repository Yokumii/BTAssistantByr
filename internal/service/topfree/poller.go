package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

var (
	// ErrSourceRequired indicates that no TopFree source was supplied.
	ErrSourceRequired = errors.New("TopFree source is required")
	// ErrHandlerRequired indicates that no result handler was supplied.
	ErrHandlerRequired = errors.New("TopFree handler is required")
	// ErrInvalidInterval indicates that the tracker refresh interval is not positive.
	ErrInvalidInterval = errors.New("TopFree refresh interval must be positive")
	// ErrInvalidQueueCheckInterval indicates that queue state cannot be checked periodically.
	ErrInvalidQueueCheckInterval = errors.New("TopFree queue check interval must be positive")
)

// Source supplies the current TopFree torrent set.
type Source interface {
	ListTopFree(context.Context) ([]domain.Candidate, error)
}

// Handler consumes one complete TopFree snapshot.
type Handler func(context.Context, []domain.Candidate) error

// Poller refreshes tracker snapshots and checks the local queue on independent schedules.
type Poller struct {
	source             Source
	refreshInterval    time.Duration
	queueCheckInterval time.Duration
	logger             *slog.Logger
}

// SetLogger enables structured lifecycle logging. It must be called before Run.
func (p *Poller) SetLogger(logger *slog.Logger) {
	p.logger = logger
}

// NewPoller creates a TopFree poller.
func NewPoller(source Source, refreshInterval, queueCheckInterval time.Duration) (*Poller, error) {
	if source == nil {
		return nil, ErrSourceRequired
	}
	if refreshInterval <= 0 {
		return nil, ErrInvalidInterval
	}
	if queueCheckInterval <= 0 {
		return nil, ErrInvalidQueueCheckInterval
	}
	return &Poller{source: source, refreshInterval: refreshInterval, queueCheckInterval: queueCheckInterval}, nil
}

// Run blocks until cancellation or a non-network error. Exhausted transient
// failures skip the current schedule without replaying a complete strategy cycle.
func (p *Poller) Run(ctx context.Context, handler Handler) error {
	if handler == nil {
		return ErrHandlerRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	nextRefresh := time.Now()
	var nextQueueCheck time.Time
	var snapshot []domain.Candidate
	snapshotValid := false
	refreshRound := int64(0)
	queueRound := int64(0)

	for {
		now := time.Now()
		if !nextRefresh.After(now) {
			refreshRound++
			started := time.Now()
			if p.logger != nil {
				p.logger.DebugContext(ctx, "TopFree tracker refresh started", "round", refreshRound)
			}
			candidates, err := p.source.ListTopFree(ctx)
			nextRefresh = time.Now().Add(p.refreshInterval)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if !netretry.IsTransient(err) {
					return fmt.Errorf("list TopFree torrents: %w", err)
				}
				snapshot = nil
				snapshotValid = false
				nextQueueCheck = time.Time{}
				if p.logger != nil {
					p.logger.WarnContext(ctx, "TopFree tracker refresh skipped after network retries",
						"round", refreshRound,
						"next_refresh_in", p.refreshInterval,
						"error", err,
					)
				}
				continue
			}
			snapshot = append(snapshot[:0], candidates...)
			snapshotValid = true
			if p.logger != nil {
				p.logger.InfoContext(ctx, "TopFree snapshot refreshed", "round", refreshRound, "candidates", len(snapshot), "duration", time.Since(started))
			}
			if err := p.handle(ctx, handler, snapshot, "tracker_refresh", refreshRound); err != nil {
				return err
			}
			nextQueueCheck = time.Now().Add(p.queueCheckInterval)
			continue
		}

		if snapshotValid && !nextQueueCheck.After(now) {
			queueRound++
			if p.logger != nil {
				p.logger.DebugContext(ctx, "TopFree queue check started", "round", queueRound, "cached_candidates", len(snapshot))
			}
			if err := p.handle(ctx, handler, snapshot, "queue_check", queueRound); err != nil {
				return err
			}
			nextQueueCheck = time.Now().Add(p.queueCheckInterval)
			continue
		}

		wakeAt := nextRefresh
		if snapshotValid && nextQueueCheck.Before(wakeAt) {
			wakeAt = nextQueueCheck
		}
		timer := time.NewTimer(time.Until(wakeAt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *Poller) handle(ctx context.Context, handler Handler, snapshot []domain.Candidate, trigger string, round int64) error {
	started := time.Now()
	err := handler(ctx, append([]domain.Candidate(nil), snapshot...))
	if err == nil {
		if p.logger != nil {
			p.logger.DebugContext(ctx, "TopFree queue cycle completed", "trigger", trigger, "round", round, "duration", time.Since(started))
		}
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if !netretry.IsTransient(err) {
		return fmt.Errorf("handle TopFree torrents: %w", err)
	}
	if p.logger != nil {
		p.logger.WarnContext(ctx, "TopFree queue cycle skipped after network retries",
			"trigger", trigger,
			"round", round,
			"next_queue_check_in", p.queueCheckInterval,
			"error", err,
		)
	}
	return nil
}
