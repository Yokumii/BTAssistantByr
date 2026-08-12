package byr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	SessionCacheFileName = "byr_session.json"
	sessionCacheVersion  = 1
	maxSessionCacheSize  = 64 << 10
)

var (
	ErrSessionCachePathRequired = errors.New("BYR session cache path is required")
	ErrInvalidSessionCache      = errors.New("invalid BYR session cache")
)

// SessionCache persists the minimum BYR cookies required to reuse a login.
type SessionCache struct {
	path       string
	backupPath string
}

type sessionCacheDocument struct {
	Version   int    `json:"version"`
	BaseURL   string `json:"base_url"`
	Username  string `json:"username"`
	AuthToken string `json:"auth_token"`
	SessionID string `json:"session_id"`
}

// NewSessionCache stores BYR session cookies below dataDir.
func NewSessionCache(dataDir string) (*SessionCache, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, ErrSessionCachePathRequired
	}
	path := filepath.Join(dataDir, SessionCacheFileName)
	return &SessionCache{path: path, backupPath: path + ".backup"}, nil
}

// Path returns the cache path without accessing the filesystem.
func (cache *SessionCache) Path() string {
	return cache.path
}

// Load returns a cached session only when it belongs to this tracker account.
func (cache *SessionCache) Load(baseURL, username string) (Credential, bool, error) {
	document, found, err := cache.readDocument(cache.path)
	if err != nil {
		return Credential{}, false, err
	}
	if !found {
		document, found, err = cache.readDocument(cache.backupPath)
		if err != nil || !found {
			return Credential{}, false, err
		}
	}
	identityURL, identityUsername, err := sessionCacheIdentity(baseURL, username)
	if err != nil {
		return Credential{}, false, err
	}
	if document.BaseURL != identityURL || document.Username != identityUsername {
		return Credential{}, false, nil
	}
	return Credential{AuthToken: document.AuthToken, SessionID: document.SessionID}, true, nil
}

// Save atomically replaces the cached session where the platform permits it.
func (cache *SessionCache) Save(baseURL, username string, credential Credential) error {
	identityURL, identityUsername, err := sessionCacheIdentity(baseURL, username)
	if err != nil {
		return err
	}
	credential, err = normalizeCredential(credential)
	if err != nil || credential.SessionID == "" {
		return ErrInvalidSessionCache
	}
	document := sessionCacheDocument{
		Version:   sessionCacheVersion,
		BaseURL:   identityURL,
		Username:  identityUsername,
		AuthToken: credential.AuthToken,
		SessionID: credential.SessionID,
	}

	directory := filepath.Dir(cache.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create BYR session cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".byr-session-*")
	if err != nil {
		return fmt.Errorf("create temporary BYR session cache: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict temporary BYR session cache permissions: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(document); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode BYR session cache: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync BYR session cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close BYR session cache: %w", err)
	}
	if err := cache.replace(temporaryPath); err != nil {
		return err
	}
	removeTemporary = false
	if err := os.Chmod(cache.path, 0o600); err != nil {
		return fmt.Errorf("restrict BYR session cache permissions: %w", err)
	}
	return nil
}

func (cache *SessionCache) readDocument(path string) (sessionCacheDocument, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return sessionCacheDocument{}, false, nil
	}
	if err != nil {
		return sessionCacheDocument{}, false, fmt.Errorf("inspect BYR session cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return sessionCacheDocument{}, false, ErrInvalidSessionCache
	}
	if info.Size() > maxSessionCacheSize {
		return sessionCacheDocument{}, false, ErrInvalidSessionCache
	}
	file, err := os.Open(path)
	if err != nil {
		return sessionCacheDocument{}, false, fmt.Errorf("open BYR session cache: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxSessionCacheSize))
	decoder.DisallowUnknownFields()
	var document sessionCacheDocument
	if err := decoder.Decode(&document); err != nil {
		return sessionCacheDocument{}, false, fmt.Errorf("%w: decode failed", ErrInvalidSessionCache)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return sessionCacheDocument{}, false, ErrInvalidSessionCache
	}
	if document.Version != sessionCacheVersion ||
		strings.TrimSpace(document.BaseURL) == "" ||
		strings.TrimSpace(document.Username) == "" ||
		strings.TrimSpace(document.AuthToken) == "" ||
		strings.TrimSpace(document.SessionID) == "" {
		return sessionCacheDocument{}, false, ErrInvalidSessionCache
	}
	document.AuthToken = strings.TrimSpace(document.AuthToken)
	document.SessionID = strings.TrimSpace(document.SessionID)
	return document, true, nil
}

func (cache *SessionCache) replace(temporaryPath string) error {
	if err := os.Rename(temporaryPath, cache.path); err == nil {
		return nil
	}
	_ = os.Remove(cache.backupPath)
	if err := os.Rename(cache.path, cache.backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup existing BYR session cache: %w", err)
	}
	if err := os.Rename(temporaryPath, cache.path); err != nil {
		_ = os.Rename(cache.backupPath, cache.path)
		return fmt.Errorf("replace BYR session cache: %w", err)
	}
	_ = os.Remove(cache.backupPath)
	return nil
}

func sessionCacheIdentity(baseURL, username string) (string, string, error) {
	parsed, err := parseBaseURL(baseURL)
	if err != nil {
		return "", "", err
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", ErrUsernameRequired
	}
	return parsed.String(), username, nil
}
