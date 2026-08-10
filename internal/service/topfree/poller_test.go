package topfree

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

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
	}), time.Millisecond)
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
	}), time.Second)
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
	}), time.Second)
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
	}), time.Second)
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

	if _, err := NewPoller(nil, time.Second); !errors.Is(err, ErrSourceRequired) {
		t.Errorf("nil source error = %v", err)
	}
	if _, err := NewPoller(sourceFunc(func(context.Context) ([]domain.Candidate, error) { return nil, nil }), 0); !errors.Is(err, ErrInvalidInterval) {
		t.Errorf("zero interval error = %v", err)
	}
}
