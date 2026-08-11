// Package netretry classifies transient network failures and retries one external operation.
package netretry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

var ErrTransient = errors.New("transient network failure")

// Transient marks an HTTP response failure as safe to retry.
func Transient(err error) error {
	if err == nil || errors.Is(err, ErrTransient) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrTransient, err)
}

// TransientStatus reports statuses normally caused by temporary server or rate-limit failures.
func TransientStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooEarly ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

// IsTransient distinguishes retryable transport/status failures from business errors.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrTransient) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

// Policy retries one concrete external operation without replaying a complete strategy cycle.
type Policy struct {
	Retries int
	Delay   time.Duration
	Logger  *slog.Logger
}

// Do retries an operation only for transient network failures.
func (p Policy) Do(ctx context.Context, operation string, run func() error) error {
	_, err := DoValue(ctx, p, operation, func() (struct{}, error) {
		return struct{}{}, run()
	})
	return err
}

// DoValue is Do for operations returning a value.
func DoValue[T any](ctx context.Context, policy Policy, operation string, run func() (T, error)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, err := run()
		if err == nil {
			return value, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, ctxErr
		}
		if !IsTransient(err) || attempt > policy.Retries {
			return zero, err
		}
		if policy.Logger != nil {
			policy.Logger.WarnContext(ctx, "network request failed; retrying",
				"operation", operation,
				"attempt", attempt,
				"max_attempts", policy.Retries+1,
				"retry_in", policy.Delay,
				"error", err,
			)
		}
		if policy.Delay <= 0 {
			continue
		}
		timer := time.NewTimer(policy.Delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}
