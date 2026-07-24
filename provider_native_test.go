package main

import (
	"context"
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

func TestNativeCatalogAppearsAfterLoginAndExcludesAnthropicSubscriptionOAuth(t *testing.T) {
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
	ids := map[string]bool{}
	for _, model := range codexNamedModelsWithAuth(&Config{}, manager) {
		ids[model.ID] = true
	}
	if !ids["kimi/kimi-k2.7-code"] || !ids["xai/grok-4.5"] {
		t.Fatalf("authenticated native models missing: %+v", ids)
	}
	for id := range ids {
		if len(id) >= len("anthropic/") && id[:len("anthropic/")] == "anthropic/" {
			t.Fatalf("unsupported Anthropic subscription OAuth model was advertised: %s", id)
		}
	}
}
