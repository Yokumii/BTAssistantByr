package netretry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestPolicyRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	value, err := DoValue(context.Background(), Policy{Retries: 2}, "fixture", func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", Transient(errors.New("temporary"))
		}
		return "ok", nil
	})
	if err != nil || value != "ok" || attempts != 3 {
		t.Fatalf("value = %q, attempts = %d, error = %v", value, attempts, err)
	}
}

func TestPolicyDoesNotRetryBusinessFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("invalid response")
	attempts := 0
	err := (Policy{Retries: 3}).Do(context.Background(), "fixture", func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) || attempts != 1 {
		t.Fatalf("attempts = %d, error = %v", attempts, err)
	}
}

func TestPolicyStopsAfterConfiguredRetries(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := (Policy{Retries: 2}).Do(context.Background(), "fixture", func() error {
		attempts++
		return Transient(errors.New("temporary"))
	})
	if !IsTransient(err) || attempts != 3 {
		t.Fatalf("attempts = %d, error = %v", attempts, err)
	}
}

func TestPolicyRetryDelayHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (Policy{Retries: 2, Delay: time.Hour}).Do(ctx, "fixture", func() error {
		return Transient(errors.New("temporary"))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestTransientStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		if !TransientStatus(status) {
			t.Errorf("TransientStatus(%d) = false", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if TransientStatus(status) {
			t.Errorf("TransientStatus(%d) = true", status)
		}
	}
}

func TestTrackerRetriesOneOperationWithoutReplayingCaller(t *testing.T) {
	t.Parallel()

	delegate := &retryTrackerFixture{failures: 2}
	client := NewTracker(delegate, Policy{Retries: 2})
	candidates, err := client.ListTopFree(context.Background())
	if err != nil || len(candidates) != 1 || delegate.attempts != 3 {
		t.Fatalf("candidates = %+v, attempts = %d, error = %v", candidates, delegate.attempts, err)
	}
}

type retryTrackerFixture struct {
	failures int
	attempts int
}

func (fixture *retryTrackerFixture) ListTopFree(context.Context) ([]domain.Candidate, error) {
	fixture.attempts++
	if fixture.attempts <= fixture.failures {
		return nil, Transient(errors.New("temporary"))
	}
	return []domain.Candidate{{ID: "1"}}, nil
}

func (*retryTrackerFixture) DownloadURL(context.Context, domain.TorrentID) (string, error) {
	return "https://example.invalid/torrent", nil
}
