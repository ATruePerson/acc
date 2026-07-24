package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNativeProviderAuthNeverMixesCredentials(t *testing.T) {
	store := newMemoryCredentialStore()
	ctx := context.Background()
	for _, credential := range []authCredential{
		{Provider: "kimi", AccessToken: "kimi-token", ExpiresAt: time.Now().Add(time.Hour)},
		{Provider: "xai", AccessToken: "xai-token", ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := store.Save(ctx, credential); err != nil {
			t.Fatal(err)
		}
	}
	manager := newAuthManager(store, nil)
	kimi, err := resolveProviderRuntime(ctx, &Config{}, manager, "kimi", false)
	if err != nil {
		t.Fatal(err)
	}
	xai, err := resolveProviderRuntime(ctx, &Config{}, manager, "xai", false)
	if err != nil {
		t.Fatal(err)
	}
	if kimi.BearerToken != "kimi-token" || xai.BearerToken != "xai-token" || kimi.BearerToken == xai.BearerToken {
		t.Fatalf("provider tokens mixed: kimi=%q xai=%q", kimi.BearerToken, xai.BearerToken)
	}
}

func TestAnthropicAPIKeyTakesStablePrecedenceOverOAuthCopy(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "stable-api-key")
	store := newMemoryCredentialStore()
	if err := store.Save(context.Background(), authCredential{
		Provider: "anthropic", AccessToken: "oauth-copy", ExpiresAt: time.Now().Add(time.Hour), Origin: "claude-code-import",
	}); err != nil {
		t.Fatal(err)
	}
	runtimeProvider, err := resolveProviderRuntime(context.Background(), &Config{}, newAuthManager(store, nil), "anthropic", false)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeProvider.APIKey != "stable-api-key" || runtimeProvider.BearerToken != "" || runtimeProvider.Adapter != providerAdapterAnthropic {
		t.Fatalf("Anthropic auth precedence = %+v", runtimeProvider)
	}
}

func TestNativeLoginDoesNotAutoSelectModels(t *testing.T) {
	store := newMemoryCredentialStore()
	now := time.Now().Add(time.Hour)
	for _, credential := range []authCredential{
		{Provider: "kimi", AccessToken: "kimi", ExpiresAt: now},
		{Provider: "xai", AccessToken: "xai", ExpiresAt: now},
		{Provider: "anthropic", AccessToken: "oauth-copy", ExpiresAt: now, Origin: "claude-code-import"},
	} {
		if err := store.Save(context.Background(), credential); err != nil {
			t.Fatal(err)
		}
	}
	manager := newAuthManager(store, nil)
	selected := codexNamedModels(codexTestConfig())
	withLogin := codexNamedModelsWithAuth(codexTestConfig(), manager)
	if len(withLogin) != len(selected) {
		t.Fatalf("login expanded selector from %d to %d models", len(selected), len(withLogin))
	}
	for i := range selected {
		if withLogin[i].ID != selected[i].ID {
			t.Fatalf("selector[%d] = %q, want %q", i, withLogin[i].ID, selected[i].ID)
		}
		for _, prefix := range []string{"kimi/", "xai/", "anthropic/"} {
			if strings.HasPrefix(withLogin[i].ID, prefix) {
				t.Fatalf("unselected native model was advertised after login: %s", withLogin[i].ID)
			}
		}
	}
}
