package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ATruePerson/acc/codex"
)

const providerModelCacheVersion = 1

type providerModelCache struct {
	Version   int                                `json:"version"`
	Providers map[string]providerModelCacheEntry `json:"providers"`
}

type providerModelCacheEntry struct {
	RefreshedAt time.Time             `json:"refreshed_at"`
	Models      []nativeProviderModel `json:"models"`
}

func providerModelCachePath() string {
	return filepath.Join(accDir(), "provider-models.json")
}

func discoverProviderModels(ctx context.Context, client *http.Client, runtime providerRuntime) ([]nativeProviderModel, error) {
	endpoint := strings.TrimRight(runtime.BaseURL, "/") + "/models"
	if runtime.Adapter == providerAdapterAnthropic {
		endpoint = strings.TrimRight(runtime.BaseURL, "/") + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if runtime.Adapter == providerAdapterAnthropic {
		req.Header.Set("x-api-key", runtime.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		token := runtime.BearerToken
		if token == "" {
			token = runtime.APIKey
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("model discovery for %s returned %d: %s", runtime.ID, resp.StatusCode, redactSecrets(string(body)))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse %s model catalog: %w", runtime.ID, err)
	}
	seen := map[string]bool{}
	var models []nativeProviderModel
	for _, row := range append(payload.Data, payload.Models...) {
		id := strings.TrimSpace(row.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, capabilitiesForDiscoveredModel(runtime.ID, id))
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("model discovery for %s returned no model IDs", runtime.ID)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func capabilitiesForDiscoveredModel(providerID, modelID string) nativeProviderModel {
	definition := nativeProviderDefinitions[providerID]
	for _, seed := range definition.Models {
		if seed.ID == modelID {
			return seed
		}
	}
	// Discovery proves availability, but not model-specific limits. Keep unknown
	// capabilities conservative until the provider publishes machine-readable data.
	return nativeProviderModel{ID: modelID, Context: 128000, MaxOutput: 8192, Tools: true}
}

func readProviderModelCache(path string) (providerModelCache, error) {
	cache := providerModelCache{Version: providerModelCacheVersion, Providers: map[string]providerModelCacheEntry{}}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return cache, err
	}
	if err := json.Unmarshal(body, &cache); err != nil {
		return providerModelCache{}, err
	}
	if cache.Providers == nil {
		cache.Providers = map[string]providerModelCacheEntry{}
	}
	return cache, nil
}

func writeProviderModelCache(path string, cache providerModelCache) error {
	cache.Version = providerModelCacheVersion
	if cache.Providers == nil {
		cache.Providers = map[string]providerModelCacheEntry{}
	}
	body, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return codex.AtomicWriteFile(path, append(body, '\n'), 0600)
}

func refreshProviderModelCache(ctx context.Context, client *http.Client, cfg *Config, auth *authManager, providerID string) error {
	runtime, err := resolveProviderRuntime(ctx, cfg, auth, providerID, false)
	if err != nil {
		return err
	}
	models, err := discoverProviderModels(ctx, client, runtime)
	if err != nil {
		return err
	}
	path := providerModelCachePath()
	cache, err := readProviderModelCache(path)
	if err != nil {
		return err
	}
	cache.Providers[providerID] = providerModelCacheEntry{RefreshedAt: time.Now().UTC(), Models: models}
	return writeProviderModelCache(path, cache)
}

func removeProviderModelCache(providerID string) error {
	path := providerModelCachePath()
	cache, err := readProviderModelCache(path)
	if err != nil {
		return err
	}
	delete(cache.Providers, providerID)
	return writeProviderModelCache(path, cache)
}
