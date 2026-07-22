package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingCommandRunner struct {
	stdin []byte
	name  string
	args  []string
	out   []byte
	err   error
}

func (r *recordingCommandRunner) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	r.stdin = append([]byte(nil), stdin...)
	r.name = name
	r.args = append([]string(nil), args...)
	return r.out, r.err
}

type testAuthDriver struct {
	refresh func(context.Context, authCredential) (authCredential, error)
}

func (d testAuthDriver) Refresh(ctx context.Context, credential authCredential) (authCredential, error) {
	return d.refresh(ctx, credential)
}

func TestMemoryCredentialStoreSeparatesProvidersAndDeletesOnlyTarget(t *testing.T) {
	store := newMemoryCredentialStore()
	ctx := context.Background()
	for _, provider := range []string{"kimi", "xai"} {
		if err := store.Save(ctx, authCredential{Provider: provider, AccessToken: provider + "-access"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete(ctx, "kimi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, "kimi"); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("deleted Kimi credential error = %v", err)
	}
	xai, err := store.Load(ctx, "xai")
	if err != nil || xai.AccessToken != "xai-access" {
		t.Fatalf("xAI credential was changed: %+v, %v", xai, err)
	}
}

func TestAuthManagerSingleFlightsConcurrentRefreshAndPersistsRotation(t *testing.T) {
	store := newMemoryCredentialStore()
	ctx := context.Background()
	if err := store.Save(ctx, authCredential{
		Provider: "kimi", AccessToken: "expired", RefreshToken: "refresh-1", ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	release := make(chan struct{})
	manager := newAuthManager(store, map[string]authDriver{
		"kimi": testAuthDriver{refresh: func(ctx context.Context, old authCredential) (authCredential, error) {
			calls.Add(1)
			<-release
			old.AccessToken = "fresh"
			old.RefreshToken = "refresh-2"
			old.ExpiresAt = time.Now().Add(time.Hour)
			return old, nil
		}},
	})

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := manager.AccessToken(ctx, "kimi", false)
			if err != nil {
				results <- "error:" + err.Error()
				return
			}
			results <- token
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if result != "fresh" {
			t.Fatalf("refresh result = %q", result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
	stored, err := store.Load(ctx, "kimi")
	if err != nil || stored.RefreshToken != "refresh-2" {
		t.Fatalf("rotated credential was not persisted: %+v, %v", stored, err)
	}
}

func TestAuthManagerUsesExpirySkewAndMarksPermanentFailure(t *testing.T) {
	store := newMemoryCredentialStore()
	ctx := context.Background()
	if err := store.Save(ctx, authCredential{
		Provider: "xai", AccessToken: "nearly-expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(30 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	manager := newAuthManager(store, map[string]authDriver{
		"xai": testAuthDriver{refresh: func(context.Context, authCredential) (authCredential, error) {
			return authCredential{}, &oauthError{Code: "invalid_grant", Description: "revoked"}
		}},
	})
	if _, err := manager.AccessToken(ctx, "xai", false); !errors.Is(err, errLoginRequired) {
		t.Fatalf("invalid_grant error = %v, want login required", err)
	}
	stored, err := store.Load(ctx, "xai")
	if err != nil || !stored.NeedsReauth {
		t.Fatalf("credential was not marked for reauthentication: %+v, %v", stored, err)
	}
}

func TestAuthManagerRefreshHonorsCancellation(t *testing.T) {
	store := newMemoryCredentialStore()
	ctx, cancel := context.WithCancel(context.Background())
	if err := store.Save(ctx, authCredential{Provider: "kimi", AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	manager := newAuthManager(store, map[string]authDriver{
		"kimi": testAuthDriver{refresh: func(ctx context.Context, credential authCredential) (authCredential, error) {
			<-ctx.Done()
			return authCredential{}, ctx.Err()
		}},
	})
	cancel()
	if _, err := manager.AccessToken(ctx, "kimi", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh error = %v", err)
	}
}

func TestFileCredentialStoreRequiresExplicitPathAndUsesPrivateAtomicFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	store, err := newFileCredentialStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want := authCredential{Provider: "kimi", AccessToken: "secret-access", RefreshToken: "secret-refresh"}
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "kimi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || strings.HasPrefix(entries[0].Name(), ".acc-") {
		t.Fatalf("atomic temporary file leaked: %+v", entries)
	}
}

func TestRedactSecretsRemovesCredentialsAndCallbackValues(t *testing.T) {
	input := "access_token=abc123 refresh_token=def456 code=oauth-code Authorization: Bearer bearer-token"
	redacted := redactSecrets(input)
	for _, secret := range []string{"abc123", "def456", "oauth-code", "bearer-token"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted output leaked %q: %s", secret, redacted)
		}
	}
}

func TestKeychainStoreKeepsTokensOutOfCommandArguments(t *testing.T) {
	runner := &recordingCommandRunner{}
	store := &keychainCredentialStore{runner: runner, service: authKeychainService}
	credential := authCredential{Provider: "kimi", AccessToken: "access-secret", RefreshToken: "refresh-secret"}
	if err := store.Save(context.Background(), credential); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.args, " ")
	if runner.name != "security" || !strings.Contains(joined, "add-generic-password") {
		t.Fatalf("unexpected command: %s %v", runner.name, runner.args)
	}
	if strings.Contains(joined, "access-secret") || strings.Contains(joined, "refresh-secret") {
		t.Fatalf("credential leaked into argv: %v", runner.args)
	}
	if !strings.Contains(string(runner.stdin), "access-secret") || runner.args[len(runner.args)-1] != "-w" {
		t.Fatalf("credential was not supplied through stdin: args=%v stdin=%q", runner.args, runner.stdin)
	}
}

func TestCredentialReplacementIsAtomicPerProvider(t *testing.T) {
	store := newMemoryCredentialStore()
	if err := store.Save(context.Background(), authCredential{Provider: "xai", AccessToken: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), authCredential{Provider: "xai", AccessToken: "new"}); err != nil {
		t.Fatal(err)
	}
	credential, err := store.Load(context.Background(), "xai")
	if err != nil || credential.AccessToken != "new" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}
