package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ATruePerson/acc/codex"
)

const (
	authKeychainService = "dev.atrueperson.acc.oauth"
	authExpirySkew      = 60 * time.Second
)

var (
	errCredentialNotFound = errors.New("credential not found")
	errLoginRequired      = errors.New("login required")
	providerIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

type authCredential struct {
	Provider     string    `json:"provider"`
	Kind         string    `json:"kind,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	Origin       string    `json:"origin,omitempty"`
	NeedsReauth  bool      `json:"needs_reauth,omitempty"`
}

func (c authCredential) usable(now time.Time) bool {
	if c.NeedsReauth || c.AccessToken == "" {
		return false
	}
	return c.ExpiresAt.IsZero() || c.ExpiresAt.After(now.Add(authExpirySkew))
}

type credentialStore interface {
	Load(context.Context, string) (authCredential, error)
	Save(context.Context, authCredential) error
	Delete(context.Context, string) error
}

type memoryCredentialStore struct {
	mu    sync.RWMutex
	items map[string]authCredential
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{items: make(map[string]authCredential)}
}

func (s *memoryCredentialStore) Load(_ context.Context, provider string) (authCredential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credential, ok := s.items[provider]
	if !ok {
		return authCredential{}, errCredentialNotFound
	}
	return credential, nil
}

func (s *memoryCredentialStore) Save(_ context.Context, credential authCredential) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	s.mu.Lock()
	s.items[credential.Provider] = credential
	s.mu.Unlock()
	return nil
}

func (s *memoryCredentialStore) Delete(_ context.Context, provider string) error {
	s.mu.Lock()
	delete(s.items, provider)
	s.mu.Unlock()
	return nil
}

type fileCredentialStore struct {
	dir string
}

func newFileCredentialStore(dir string) (*fileCredentialStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("credential file store requires an explicit absolute directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	return &fileCredentialStore{dir: dir}, nil
}

func (s *fileCredentialStore) path(provider string) (string, error) {
	if !providerIDPattern.MatchString(provider) {
		return "", fmt.Errorf("invalid provider ID %q", provider)
	}
	return filepath.Join(s.dir, provider+".json"), nil
}

func (s *fileCredentialStore) Load(_ context.Context, provider string) (authCredential, error) {
	path, err := s.path(provider)
	if err != nil {
		return authCredential{}, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return authCredential{}, errCredentialNotFound
	}
	if err != nil {
		return authCredential{}, err
	}
	var credential authCredential
	if err := json.Unmarshal(b, &credential); err != nil {
		return authCredential{}, fmt.Errorf("decode %s credential: %w", provider, err)
	}
	if credential.Provider != provider {
		return authCredential{}, fmt.Errorf("credential provider mismatch")
	}
	return credential, nil
}

func (s *fileCredentialStore) Save(_ context.Context, credential authCredential) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	path, err := s.path(credential.Provider)
	if err != nil {
		return err
	}
	b, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	return codex.AtomicWriteFile(path, append(b, '\n'), 0600)
}

func (s *fileCredentialStore) Delete(_ context.Context, provider string) error {
	path, err := s.path(provider)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type commandRunner interface {
	Run(context.Context, []byte, string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	return cmd.Output()
}

type keychainCredentialStore struct {
	runner  commandRunner
	service string
}

func newKeychainCredentialStore() *keychainCredentialStore {
	return &keychainCredentialStore{runner: execCommandRunner{}, service: authKeychainService}
}

func (s *keychainCredentialStore) Load(ctx context.Context, provider string) (authCredential, error) {
	if !providerIDPattern.MatchString(provider) {
		return authCredential{}, fmt.Errorf("invalid provider ID %q", provider)
	}
	out, err := s.runner.Run(ctx, nil, "security", "find-generic-password", "-a", provider, "-s", s.service, "-w")
	if err != nil {
		return authCredential{}, errCredentialNotFound
	}
	var credential authCredential
	if err := json.Unmarshal(bytes.TrimSpace(out), &credential); err != nil {
		return authCredential{}, fmt.Errorf("decode Keychain credential for %s: %w", provider, err)
	}
	if credential.Provider != provider {
		return authCredential{}, fmt.Errorf("credential provider mismatch")
	}
	return credential, nil
}

func (s *keychainCredentialStore) Save(ctx context.Context, credential authCredential) error {
	if err := validateCredential(credential); err != nil {
		return err
	}
	b, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	// Use security's interactive command mode and -X hexadecimal password data.
	// The credential stays on stdin and never appears in argv or shell history.
	command := fmt.Sprintf("add-generic-password -U -a %s -s %s -X %s\n", credential.Provider, s.service, hex.EncodeToString(b))
	_, err = s.runner.Run(ctx, []byte(command), "security", "-i")
	if err != nil {
		return fmt.Errorf("save %s credential in Keychain: %w", credential.Provider, err)
	}
	return nil
}

func (s *keychainCredentialStore) Delete(ctx context.Context, provider string) error {
	if !providerIDPattern.MatchString(provider) {
		return fmt.Errorf("invalid provider ID %q", provider)
	}
	_, err := s.runner.Run(ctx, nil, "security", "delete-generic-password", "-a", provider, "-s", s.service)
	if err != nil {
		if _, loadErr := s.Load(ctx, provider); errors.Is(loadErr, errCredentialNotFound) {
			return nil
		}
		return fmt.Errorf("delete %s credential from Keychain: %w", provider, err)
	}
	return nil
}

func validateCredential(credential authCredential) error {
	if !providerIDPattern.MatchString(credential.Provider) {
		return fmt.Errorf("invalid provider ID %q", credential.Provider)
	}
	if credential.AccessToken == "" && !credential.NeedsReauth {
		return fmt.Errorf("credential for %s has no access token", credential.Provider)
	}
	return nil
}

func defaultCredentialStore() (credentialStore, string, error) {
	if strings.EqualFold(os.Getenv("ACC_AUTH_STORE"), "file") {
		path := strings.TrimSpace(os.Getenv("ACC_AUTH_FILE_DIR"))
		store, err := newFileCredentialStore(path)
		return store, "explicit file store", err
	}
	if runtime.GOOS != "darwin" {
		return nil, "unavailable", fmt.Errorf("secure credential storage is unavailable on %s; explicitly set ACC_AUTH_STORE=file and ACC_AUTH_FILE_DIR to opt into private file storage", runtime.GOOS)
	}
	if _, err := exec.LookPath("security"); err != nil {
		return nil, "unavailable", fmt.Errorf("macOS Keychain command is unavailable")
	}
	return newKeychainCredentialStore(), "macOS Keychain", nil
}

type authDriver interface {
	Refresh(context.Context, authCredential) (authCredential, error)
}

type authRefreshCall struct {
	done       chan struct{}
	credential authCredential
	err        error
}

type authManager struct {
	store      credentialStore
	storeName  string
	drivers    map[string]authDriver
	mu         sync.Mutex
	refreshing map[string]*authRefreshCall
	now        func() time.Time
}

func newAuthManager(store credentialStore, drivers map[string]authDriver) *authManager {
	return &authManager{
		store: store, drivers: drivers, refreshing: make(map[string]*authRefreshCall), now: time.Now,
	}
}

func (m *authManager) AccessToken(ctx context.Context, provider string, forceRefresh bool) (string, error) {
	if m == nil || m.store == nil {
		return "", fmt.Errorf("%w for %s", errLoginRequired, provider)
	}
	credential, err := m.store.Load(ctx, provider)
	if err != nil {
		if errors.Is(err, errCredentialNotFound) {
			return "", fmt.Errorf("%w for %s", errLoginRequired, provider)
		}
		return "", err
	}
	if !forceRefresh && credential.usable(m.now()) {
		return credential.AccessToken, nil
	}
	if credential.NeedsReauth || credential.RefreshToken == "" {
		return "", fmt.Errorf("%w for %s", errLoginRequired, provider)
	}
	driver, ok := m.drivers[provider]
	if !ok {
		return "", fmt.Errorf("%w for %s", errLoginRequired, provider)
	}

	m.mu.Lock()
	if existing := m.refreshing[provider]; existing != nil {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-existing.done:
			if existing.err != nil {
				return "", existing.err
			}
			return existing.credential.AccessToken, nil
		}
	}
	call := &authRefreshCall{done: make(chan struct{})}
	m.refreshing[provider] = call
	m.mu.Unlock()

	fresh, refreshErr := driver.Refresh(ctx, credential)
	if refreshErr == nil {
		fresh.Provider = provider
		fresh.NeedsReauth = false
		if fresh.RefreshToken == "" {
			fresh.RefreshToken = credential.RefreshToken
		}
		if fresh.Kind == "" {
			fresh.Kind = credential.Kind
		}
		if fresh.Origin == "" {
			fresh.Origin = credential.Origin
		}
		refreshErr = m.store.Save(ctx, fresh)
	} else if isPermanentOAuthError(refreshErr) {
		credential.NeedsReauth = true
		_ = m.store.Save(context.WithoutCancel(ctx), credential)
		refreshErr = fmt.Errorf("%w for %s", errLoginRequired, provider)
	}

	m.mu.Lock()
	call.credential, call.err = fresh, refreshErr
	delete(m.refreshing, provider)
	close(call.done)
	m.mu.Unlock()
	if refreshErr != nil {
		return "", refreshErr
	}
	return fresh.AccessToken, nil
}

func (m *authManager) Save(ctx context.Context, credential authCredential) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("secure credential store unavailable")
	}
	return m.store.Save(ctx, credential)
}

func (m *authManager) Delete(ctx context.Context, provider string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("secure credential store unavailable")
	}
	return m.store.Delete(ctx, provider)
}

func (m *authManager) Status(ctx context.Context, provider string) (authCredential, error) {
	if m == nil || m.store == nil {
		return authCredential{}, fmt.Errorf("secure credential store unavailable")
	}
	return m.store.Load(ctx, provider)
}

type oauthError struct {
	Status      int
	Code        string
	Description string
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("OAuth %s: %s", e.Code, redactSecrets(e.Description))
	}
	if e.Code != "" {
		return "OAuth " + e.Code
	}
	return fmt.Sprintf("OAuth HTTP %d", e.Status)
}

func isPermanentOAuthError(err error) bool {
	var oauthErr *oauthError
	if !errors.As(err, &oauthErr) {
		return false
	}
	switch oauthErr.Code {
	case "invalid_grant", "access_denied", "expired_token", "revoked_token", "refresh_token_reused":
		return true
	default:
		return false
	}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(access_token|refresh_token|authorization_code|code)=([^\s&]+)`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)([^\s]+)`),
}

func redactSecrets(value string) string {
	redacted := value
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllString(redacted, `${1}=[REDACTED]`)
	}
	return redacted
}
