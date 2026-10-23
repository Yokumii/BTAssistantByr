package topfree

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
	"github.com/Yokumii/BTAssistantByr/internal/netretry"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

func TestPollerDiscardsInvalidSnapshotsUntilRefreshRecovers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		valid []bool
	}{
		{name: "initial failures", valid: []bool{false, false, true}},
		{name: "failures after valid snapshot", valid: []bool{true, false, false, true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			const refreshInterval = 10 * time.Minute
			var fetches, recoveredHandles, failedWaits int
			poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
				fetches++
				if fetches > len(test.valid) {
					return nil, errors.New("unexpected extra refresh")
				}
				if !test.valid[fetches-1] {
					// Partial results must never reach the handler either.
					return []domain.Candidate{{ID: "partial"}}, fmt.Errorf("parse page: %w", port.ErrInvalidTrackerSnapshot)
				}
				return []domain.Candidate{{ID: domain.TorrentID(fmt.Sprint(fetches))}}, nil
			}), refreshInterval, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, time.October, 21, 0, 0, 0, 0, time.UTC)
			poller.now = func() time.Time { return now }
			poller.waitUntil = func(ctx context.Context, wakeAt time.Time) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !test.valid[fetches-1] {
					failedWaits++
					if want := now.Add(refreshInterval); !wakeAt.Equal(want) {
						t.Fatalf("invalid snapshot wakes at %s, want next refresh %s", wakeAt, want)
					}
				}
				now = wakeAt
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err = poller.Run(ctx, func(_ context.Context, candidates []domain.Candidate) error {
				if !test.valid[fetches-1] {
					t.Fatal("handler called while snapshot is invalid")
				}
				if len(candidates) != 1 || candidates[0].ID != domain.TorrentID(fmt.Sprint(fetches)) {
					t.Fatalf("handler received stale or partial snapshot: %v", candidates)
				}
				if fetches == len(test.valid) {
					recoveredHandles++
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || recoveredHandles != 1 || failedWaits != 2 {
				t.Fatalf("error = %v, recovered handles = %d, failed waits = %d", err, recoveredHandles, failedWaits)
			}
		})
	}
}

type sourceFunc func(context.Context) ([]domain.Candidate, error)

func (function sourceFunc) ListTopFree(ctx context.Context) ([]domain.Candidate, error) {
	return function(ctx)
}

func TestPollerRunsImmediatelyAndRepeats(t *testing.T) {
	t.Parallel()

	var fetches int
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetches++
		return []domain.Candidate{{ID: "fixture"}}, nil
	}), time.Millisecond, time.Hour)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err = poller.Run(ctx, func(_ context.Context, candidates []domain.Candidate) error {
		if len(candidates) != 1 {
			t.Fatalf("len(candidates) = %d, want 1", len(candidates))
		}
		if fetches == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetches != 2 {
		t.Errorf("fetches = %d, want 2", fetches)
	}
}

func TestPollerReturnsSourceError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("source failed")
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		return nil, wantErr
	}), time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	if err := poller.Run(context.Background(), func(context.Context, []domain.Candidate) error { return nil }); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want source error", err)
	}
}

func TestPollerReturnsHandlerError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("handler failed")
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		return nil, nil
	}), time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	if err := poller.Run(context.Background(), func(context.Context, []domain.Candidate) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want handler error", err)
	}
}

func TestPollerDoesNotFetchAfterCancellation(t *testing.T) {
	t.Parallel()

	fetched := false
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetched = true
		return nil, nil
	}), time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := poller.Run(ctx, func(context.Context, []domain.Candidate) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetched {
		t.Error("source was called after cancellation")
	}
}

func TestNewPollerValidatesDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewPoller(nil, time.Second, time.Second); !errors.Is(err, ErrSourceRequired) {
		t.Errorf("nil source error = %v", err)
	}
	if _, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) { return nil, nil }), 0, time.Second); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("zero interval error = %v", err)
	}
	if _, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) { return nil, nil }), time.Second, 0); !errors.Is(err, ErrInvalidQueueCheckInterval) {
		t.Errorf("zero queue interval error = %v", err)
	}
}

func TestPollerChecksQueueWithoutRefreshingTracker(t *testing.T) {
	t.Parallel()

	var fetches int
	var handles int
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetches++
		return []domain.Candidate{{ID: "fixture"}}, nil
	}), time.Hour, time.Millisecond)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = poller.Run(ctx, func(context.Context, []domain.Candidate) error {
		handles++
		if handles == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetches != 1 || handles != 2 {
		t.Fatalf("fetches = %d, handles = %d, want 1 and 2", fetches, handles)
	}
}

func TestPollerSkipsFailedNetworkRefreshUntilNextRefresh(t *testing.T) {
	t.Parallel()

	var fetches int
	var handles int
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetches++
		if fetches == 1 {
			return nil, netretry.Transient(errors.New("tracker unavailable"))
		}
		return []domain.Candidate{{ID: "fixture"}}, nil
	}), time.Millisecond, time.Hour)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = poller.Run(ctx, func(context.Context, []domain.Candidate) error {
		handles++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetches != 2 || handles != 1 {
		t.Fatalf("fetches = %d, handles = %d, want 2 and 1", fetches, handles)
	}
}

func TestPollerSkipsTransientHandlerFailureAndChecksQueueAgain(t *testing.T) {
	t.Parallel()

	var fetches int
	var handles int
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetches++
		return []domain.Candidate{{ID: "fixture"}}, nil
	}), time.Hour, time.Millisecond)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = poller.Run(ctx, func(context.Context, []domain.Candidate) error {
		handles++
		if handles == 1 {
			return netretry.Transient(errors.New("qBittorrent unavailable"))
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetches != 1 || handles != 2 {
		t.Fatalf("fetches = %d, handles = %d, want 1 and 2", fetches, handles)
	}
}

func TestPollerDefersDiskShortageAndChecksQueueAgain(t *testing.T) {
	t.Parallel()

	var fetches int
	var handles int
	poller, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) {
		fetches++
		return []domain.Candidate{{ID: "fixture"}}, nil
	}), time.Hour, time.Millisecond)
	if err != nil {
		t.Fatalf("NewPoller() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = poller.Run(ctx, func(context.Context, []domain.Candidate) error {
		handles++
		if handles == 1 {
			return fmt.Errorf("verify cleanup: %w", ErrInsufficientDiskSpace)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if fetches != 1 || handles != 2 {
		t.Fatalf("fetches = %d, handles = %d, want 1 and 2", fetches, handles)
	}
}
