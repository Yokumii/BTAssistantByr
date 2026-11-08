package netretry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/domain"
)

func TestPolicyLogsRecoveryAndTargetContext(t *testing.T) {
	var logs bytes.Buffer
	policy := Policy{Retries: 1, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	attempts := 0
	err := policy.withLogAttrs("torrent_id", "42").Do(context.Background(), "read publication metadata", func() error {
		attempts++
		if attempts == 1 {
			return Transient(errors.New("temporary"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"network operation started", "network request failed; retrying", "network operation completed", "torrent_id=42", "attempt=2", "max_attempts=2", "duration="} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing %q in %s", expected, logs.String())
		}
	}
	if strings.Contains(logs.String(), "retries exhausted") {
		t.Errorf("successful operation logged exhaustion: %s", logs.String())
	}
}

func TestPolicyLogsFinalExhaustion(t *testing.T) {
	var logs bytes.Buffer
	policy := Policy{Retries: 1, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	err := policy.withLogAttrs("hashes", []domain.TorrentHash{"fixture-hash"}).Do(context.Background(), "delete torrents", func() error {
		return Transient(errors.New("temporary"))
	})
	if !IsTransient(err) {
		t.Fatalf("classification lost: %v", err)
	}
	for _, expected := range []string{"network operation retries exhausted", "hashes=[fixture-hash]", "attempt=2", "max_attempts=2", "error="} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing %q in %s", expected, logs.String())
		}
	}
	if strings.Contains(logs.String(), "network operation completed") {
		t.Errorf("failed operation logged completion: %s", logs.String())
	}
}

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
