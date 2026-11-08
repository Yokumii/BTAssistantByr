package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Yokumii/BTAssistantByr/internal/netretry"
)

func TestNotifyRetriesOnlyEmailWithStableIdempotencyKey(t *testing.T) {
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer re_secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected Resend request")
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if len(keys) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"id":"email-123"}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, 2)
	if err := client.Notify(context.Background(), "写操作完成", "删除成功\n空间变化"); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("email retries changed payload or idempotency key")
	}
	var payload struct {
		From          string
		To            []string
		Subject, Text string
	}
	if err := json.Unmarshal(bodies[0], &payload); err != nil || payload.From != "bt@example.com" || len(payload.To) != 1 || payload.To[0] != "you@example.com" || payload.Subject != "写操作完成" || payload.Text != "删除成功\n空间变化" {
		t.Fatal("unexpected email payload")
	}
	if err := client.Notify(context.Background(), "another execution", "body"); err != nil {
		t.Fatal(err)
	}
	if keys[2] == keys[0] {
		t.Fatal("different summaries reused the same idempotency key")
	}
}

func TestNotifyRetriesConcurrentIdempotencyConflict(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"name":"concurrent_idempotent_requests"}`)
			return
		}
		io.WriteString(w, `{"id":"email-123"}`)
	}))
	defer server.Close()
	if err := newTestClient(t, server, 1).Notify(context.Background(), "summary", "body"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestNotifyRejectsPermanentErrorsAndInvalidReceipts(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `{"message":"re_secret private-email passkey=secret"}`},
		{"redirect", 307, "private-email"},
		{"missing receipt", 200, `{}`},
		{"invalid JSON", 200, "private-email"},
		{"oversized receipt", 200, strings.Repeat("x", (64<<10)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			err := newTestClient(t, server, 2).Notify(context.Background(), "summary", "body")
			if err == nil || calls != 1 {
				t.Fatalf("error = %v, calls = %d; want one failed attempt", err, calls)
			}
			for _, secret := range []string{"re_secret", "private-email", "passkey=secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("notification error leaked sensitive response data")
				}
			}
		})
	}
}

func TestNotifyHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("canceled notification reached server")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newTestClient(t, server, 2).Notify(ctx, "summary", "body"); err != context.Canceled {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server, retries int) *Client {
	t.Helper()
	client := NewClient("re_secret", "bt@example.com", []string{"you@example.com"}, time.Second, netretry.Policy{Retries: retries})
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = redirectTestTransport{target: target, base: server.Client().Transport}
	return client
}

type redirectTestTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport redirectTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = transport.target.Scheme, transport.target.Host
	return transport.base.RoundTrip(r)
}
