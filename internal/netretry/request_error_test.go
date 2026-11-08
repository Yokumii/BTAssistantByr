package netretry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRequestErrorHidesURLSecretsAndPreservesCause(t *testing.T) {
	transportErr := &url.Error{
		Op:  "Get",
		URL: "https://alice:password@tracker.example/download.php?id=42&passkey=private-key#private-fragment",
		Err: context.DeadlineExceeded,
	}
	err := RequestError("read tracker metadata", fmt.Errorf("transport: %w", transportErr))
	if !errors.Is(err, context.DeadlineExceeded) || !IsTransient(err) {
		t.Fatalf("transport classification lost: %v", err)
	}
	var original *url.Error
	if !errors.As(err, &original) || original != transportErr {
		t.Fatalf("underlying url.Error was not preserved")
	}
	for _, secret := range []string{"alice", "password", "id=42", "passkey", "private-key", "private-fragment"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("request error contains %q: %s", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "read tracker metadata: Get https://tracker.example/download.php: context deadline exceeded") {
		t.Fatalf("safe request context lost: %s", err)
	}
}

func TestRequestErrorHidesNestedProxyURL(t *testing.T) {
	err := RequestError("send tracker request", &url.Error{
		Op:  "Get",
		URL: "https://tracker.example/torrents.php?passkey=target-secret",
		Err: fmt.Errorf("proxy failed: %w", &url.Error{
			Op: "Connect", URL: "http://proxy-user:proxy-password@proxy.example:8080?token=proxy-secret", Err: errors.New("connection refused"),
		}),
	})
	for _, secret := range []string{"target-secret", "proxy-user", "proxy-password", "proxy-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("request error contains %q: %s", secret, err)
		}
	}
	for _, expected := range []string{"tracker.example/torrents.php", "proxy.example:8080", "connection refused"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("request error lost %q: %s", expected, err)
		}
	}
}

func TestRequestErrorHandlesInvalidURLAndNil(t *testing.T) {
	if err := RequestError("request", nil); err != nil {
		t.Fatalf("RequestError(nil) = %v", err)
	}
	cause := errors.New("invalid request")
	err := RequestError("request", &url.Error{Op: "Get", URL: "https://%private-key", Err: cause})
	if strings.Contains(err.Error(), "private-key") || !strings.Contains(err.Error(), "[invalid URL]") || !errors.Is(err, cause) {
		t.Fatalf("unexpected safe error: %s", err)
	}
}
