package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestAuthStatusAndListNeverPrintSecrets(t *testing.T) {
	store := newMemoryCredentialStore()
	_ = store.Save(context.Background(), authCredential{
		Provider: "kimi", Kind: "oauth", AccessToken: "access-secret", RefreshToken: "refresh-secret",
		Email: "person@example.com", ExpiresAt: time.Now().Add(time.Hour), Origin: "browser-oauth",
	})
	manager := newAuthManager(store, nil)
	var out bytes.Buffer
	if err := runAuthStatus(context.Background(), &out, manager, ""); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, secret := range []string{"access-secret", "refresh-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("status leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "person@example.com") || !strings.Contains(text, "kimi") {
		t.Fatalf("status omitted safe identity: %s", text)
	}
}

func TestAuthLogoutDeletesOnlyNormalizedProvider(t *testing.T) {
	store := newMemoryCredentialStore()
	_ = store.Save(context.Background(), authCredential{Provider: "xai", AccessToken: "x"})
	_ = store.Save(context.Background(), authCredential{Provider: "kimi", AccessToken: "k"})
	manager := newAuthManager(store, nil)
	if err := logoutProvider(context.Background(), manager, "grok", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Status(context.Background(), "xai"); !isCredentialMissing(err) {
		t.Fatalf("xAI credential still exists: %v", err)
	}
	if _, err := manager.Status(context.Background(), "kimi"); err != nil {
		t.Fatalf("Kimi credential was deleted: %v", err)
	}
}

func TestNormalizeAuthProviderRejectsUnknownAndMapsGrok(t *testing.T) {
	if got, err := normalizeAuthProvider("grok"); err != nil || got != "xai" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := normalizeAuthProvider("openrouter"); err == nil {
		t.Fatal("expected unsupported auth provider error")
	}
}
