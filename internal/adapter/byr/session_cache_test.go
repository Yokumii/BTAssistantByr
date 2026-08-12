package byr

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSessionCacheRoundTrip(t *testing.T) {
	t.Parallel()

	cache, err := NewSessionCache(filepath.Join(t.TempDir(), "nested", "data"))
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	want := Credential{AuthToken: "auth-token", SessionID: "session-id"}
	if err := cache.Save("HTTPS://BYR.PT/", " alice ", want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, found, err := cache.Load("https://byr.pt", "alice")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found || got.AuthToken != want.AuthToken || got.SessionID != want.SessionID {
		t.Fatalf("Load() = %+v, found=%t", got, found)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(cache.Path())
		if statErr != nil {
			t.Fatalf("Stat() error = %v", statErr)
		}
		if permissions := info.Mode().Perm(); permissions != 0o600 {
			t.Fatalf("cache permissions = %o, want 600", permissions)
		}
	}
}

func TestSessionCacheIgnoresDifferentIdentity(t *testing.T) {
	t.Parallel()

	cache, err := NewSessionCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	if err := cache.Save("https://byr.pt", "alice", Credential{AuthToken: "auth-token", SessionID: "session-id"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for _, identity := range []struct {
		baseURL  string
		username string
	}{
		{baseURL: "https://other.example", username: "alice"},
		{baseURL: "https://byr.pt", username: "bob"},
	} {
		_, found, loadErr := cache.Load(identity.baseURL, identity.username)
		if loadErr != nil || found {
			t.Fatalf("Load(%q, %q) found=%t error=%v", identity.baseURL, identity.username, found, loadErr)
		}
	}
}

func TestSessionCacheRejectsCorruption(t *testing.T) {
	t.Parallel()

	cache, err := NewSessionCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	if err := os.WriteFile(cache.Path(), []byte(`{"version":1,"auth_token":"secret"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, _, err = cache.Load("https://byr.pt", "alice")
	if !errors.Is(err, ErrInvalidSessionCache) {
		t.Fatalf("Load() error = %v, want ErrInvalidSessionCache", err)
	}
}

func TestSessionCacheDoesNotPersistAccessToken(t *testing.T) {
	t.Parallel()

	cache, err := NewSessionCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewSessionCache() error = %v", err)
	}
	credential := Credential{AccessToken: "api-access-token", AuthToken: "auth-token", SessionID: "session-id"}
	if err := cache.Save("https://byr.pt", "alice", credential); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	contents, err := os.ReadFile(cache.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) == "" || json.Valid(contents) == false {
		t.Fatalf("cache is not valid JSON: %q", contents)
	}
	if strings.Contains(string(contents), "api-access-token") {
		t.Fatal("cache persisted API access token")
	}
}
