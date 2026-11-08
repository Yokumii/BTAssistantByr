// Package resend delivers execution summaries through the Resend Email API.
package resend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/netretry"
	"github.com/Yokumii/BTAssistantByr/internal/port"
)

const endpoint = "https://api.resend.com/emails"

var _ port.Notifier = (*Client)(nil)

type Client struct {
	apiKey string
	from   string
	to     []string
	http   *http.Client
	retry  netretry.Policy
}

// NewClient uses a dedicated client; API credentials must never follow redirects.
func NewClient(apiKey, from string, to []string, timeout time.Duration, retry netretry.Policy) *Client {
	return &Client{
		apiKey: apiKey,
		from:   from,
		to:     append([]string(nil), to...),
		http: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		retry: retry,
	}
}

// Notify retries only email delivery, retaining the same payload and idempotency key.
func (c *Client) Notify(ctx context.Context, subject, text string) error {
	payload, err := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}{c.from, c.to, subject, text})
	if err != nil {
		return fmt.Errorf("encode Resend email: %w", err)
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return fmt.Errorf("create Resend idempotency key: %w", err)
	}
	idempotencyKey := "btassistantbyr/" + hex.EncodeToString(key[:])
	return c.retry.Do(ctx, "send Resend execution notification", func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create Resend email request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", idempotencyKey)
		response, err := c.http.Do(request)
		if err != nil {
			return netretry.RequestError("send Resend email POST /emails", err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			// Provider error bodies can echo email content or credentials.
			err := fmt.Errorf("Resend POST /emails returned HTTP %d", response.StatusCode)
			// 409 is also returned while an earlier request with this idempotency
			// key is still being processed.
			if netretry.TransientStatus(response.StatusCode) || response.StatusCode == http.StatusConflict {
				return netretry.Transient(err)
			}
			return err
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if err != nil {
			return netretry.RequestError("read Resend response POST /emails", err)
		}
		var result struct {
			ID string `json:"id"`
		}
		if len(body) > 64<<10 || json.Unmarshal(body, &result) != nil || result.ID == "" {
			return errors.New("Resend POST /emails returned an invalid email receipt; delivery unknown")
		}
		return nil
	})
}
