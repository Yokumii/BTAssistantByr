package topfree

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

var (
	// ErrSourceRequired indicates that no TopFree source was supplied.
	ErrSourceRequired = errors.New("TopFree source is required")
	// ErrHandlerRequired indicates that no result handler was supplied.
	ErrHandlerRequired = errors.New("TopFree handler is required")
	// ErrInvalidInterval indicates that the polling interval is not positive.
	ErrInvalidInterval = errors.New("TopFree polling interval must be positive")
)

// Source supplies the current TopFree torrent set.
type Source interface {
	ListTopFree(context.Context) ([]domain.Candidate, error)
}

// Handler consumes one complete TopFree snapshot.
type Handler func(context.Context, []domain.Candidate) error

// Poller fetches TopFree torrents immediately and then after each interval.
type Poller struct {
	source   Source
	interval time.Duration
	logger   *slog.Logger
}

// SetLogger enables structured lifecycle logging. It must be called before Run.
func (p *Poller) SetLogger(logger *slog.Logger) {
	p.logger = logger
}

// NewPoller creates a TopFree poller.
func NewPoller(source Source, interval time.Duration) (*Poller, error) {
	if source == nil {
		return nil, ErrSourceRequired
	}
	if interval <= 0 {
		return nil, ErrInvalidInterval
	}
	return &Poller{source: source, interval: interval}, nil
}

// Run blocks until the context is canceled or a fetch or handler fails.
func (p *Poller) Run(ctx context.Context, handler Handler) error {
	if handler == nil {
		return ErrHandlerRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for round := int64(1); ; round++ {
		roundStarted := time.Now()
		if p.logger != nil {
			p.logger.DebugContext(ctx, "TopFree polling round started", "round", round)
		}
		candidates, err := p.source.ListTopFree(ctx)
		if err != nil {
			return fmt.Errorf("list TopFree torrents: %w", err)
		}
		if p.logger != nil {
			p.logger.InfoContext(ctx, "TopFree snapshot fetched", "round", round, "candidates", len(candidates), "fetch_duration", time.Since(roundStarted))
		}
		if err := handler(ctx, candidates); err != nil {
			return fmt.Errorf("handle TopFree torrents: %w", err)
		}
		if p.logger != nil {
			p.logger.DebugContext(ctx, "TopFree polling round completed", "round", round, "duration", time.Since(roundStarted), "next_poll_in", p.interval)
		}

		timer := time.NewTimer(p.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
