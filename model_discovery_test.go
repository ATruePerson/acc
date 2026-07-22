package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverProviderModelsUsesProviderAuthAndRealIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request path/auth = %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta"},{"id":"alpha"},{"id":"alpha"}]}`))
	}))
	defer server.Close()
	models, err := discoverProviderModels(context.Background(), server.Client(), providerRuntime{ID: "xai", BaseURL: server.URL, Adapter: providerAdapterOpenAIChat, BearerToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "alpha" || models[1].ID != "zeta" {
		t.Fatalf("models = %#v", models)
	}
}

func TestDiscoverAnthropicModelsUsesAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") == "" {
			t.Fatalf("wrong Anthropic discovery request: %s %#v", r.URL.Path, r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-real"}]}`))
	}))
	defer server.Close()
	models, err := discoverProviderModels(context.Background(), server.Client(), providerRuntime{ID: "anthropic", BaseURL: server.URL, Adapter: providerAdapterAnthropic, APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "claude-real" {
		t.Fatalf("models = %#v", models)
	}
}

func TestProviderModelCacheIsAtomicPrivateAndContainsNoCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-models.json")
	cache := providerModelCache{Providers: map[string]providerModelCacheEntry{
		"kimi": {Models: []nativeProviderModel{{ID: "kimi-real", Tools: true}}},
	}}
	if err := writeProviderModelCache(path, cache); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	body, _ := os.ReadFile(path)
	if string(body) == "" || json.Valid(body) == false {
		t.Fatalf("invalid cache: %q", body)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "api_key", "secret"} {
		if containsFold(string(body), forbidden) {
			t.Fatalf("cache contains credential field %q: %s", forbidden, body)
		}
	}
	loaded, err := readProviderModelCache(path)
	if err != nil || loaded.Providers["kimi"].Models[0].ID != "kimi-real" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestCachedNativeCatalogOverridesStaticSeedDeterministically(t *testing.T) {
	store := newMemoryCredentialStore()
	_ = store.Save(context.Background(), authCredential{Provider: "xai", AccessToken: "token"})
	auth := newAuthManager(store, nil)
	cache := providerModelCache{Providers: map[string]providerModelCacheEntry{"xai": {Models: []nativeProviderModel{{ID: "grok-z"}, {ID: "grok-a"}}}}}
	models := nativeCodexModelsFromCache(&Config{}, auth, cache)
	var ids []string
	for _, model := range models {
		if model.Route.Provider == "xai" {
			ids = append(ids, model.ID)
		}
	}
	if len(ids) != 2 || ids[0] != "xai/grok-a" || ids[1] != "xai/grok-z" {
		t.Fatalf("xAI catalog = %v", ids)
	}
}

func containsFold(value, fragment string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(fragment))
}
