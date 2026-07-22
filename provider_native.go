package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	providerAdapterOpenAIChat = "openai-chat"
	providerAdapterAnthropic  = "anthropic"
)

type providerRuntime struct {
	ID          string
	BaseURL     string
	Adapter     string
	APIKey      string
	BearerToken string
	OAuth       bool
}

type nativeProviderDefinition struct {
	ID        string
	BaseURL   string
	Adapter   string
	APIKeyEnv string
	Models    []nativeProviderModel
}

type nativeProviderModel struct {
	ID        string
	Context   int
	MaxOutput int
	Tools     bool
	Images    bool
	Reasoning []string
}

// Static rows are only offline seeds. `acc auth login` and `acc codex setup`
// refresh authenticated providers from their live /models endpoint where
// available, then write only model IDs and capability metadata to the catalog.
var nativeProviderDefinitions = map[string]nativeProviderDefinition{
	"kimi": {
		ID: "kimi", BaseURL: "https://api.kimi.com/coding/v1", Adapter: providerAdapterOpenAIChat,
		Models: []nativeProviderModel{{ID: "kimi-k2.7-code", Context: 262144, MaxOutput: 32768, Tools: true, Images: true}},
	},
	"xai": {
		ID: "xai", BaseURL: "https://api.x.ai/v1", Adapter: providerAdapterOpenAIChat, APIKeyEnv: "XAI_API_KEY",
		Models: []nativeProviderModel{{ID: "grok-4.5", Context: 500000, MaxOutput: 32768, Tools: true, Images: true, Reasoning: []string{"low", "medium", "high"}}},
	},
	"anthropic": {
		ID: "anthropic", BaseURL: "https://api.anthropic.com", Adapter: providerAdapterAnthropic, APIKeyEnv: "ANTHROPIC_API_KEY",
		Models: []nativeProviderModel{{ID: "claude-sonnet-5", Context: 1000000, MaxOutput: 64000, Tools: true, Images: true, Reasoning: []string{"low", "medium", "high"}}},
	},
}

func newDefaultAuthManager() (*authManager, error) {
	store, storeName, err := defaultCredentialStore()
	if err != nil {
		return &authManager{storeName: storeName, drivers: map[string]authDriver{}, refreshing: make(map[string]*authRefreshCall)}, err
	}
	manager := newAuthManager(store, map[string]authDriver{
		"kimi": newKimiOAuthDriver(nil),
		"xai":  newXAIOAuthDriver(nil),
	})
	manager.storeName = storeName
	return manager, nil
}

func resolveProviderRuntime(ctx context.Context, cfg *Config, auth *authManager, providerID string, forceRefresh bool) (providerRuntime, error) {
	if cfg != nil {
		if configured, ok := cfg.Providers[providerID]; ok {
			adapter := configured.Adapter
			if adapter == "" {
				adapter = providerAdapterOpenAIChat
				if providerID == "anthropic" || strings.Contains(strings.ToLower(configured.BaseURL), "api.anthropic.com") {
					adapter = providerAdapterAnthropic
				}
			}
			if configured.APIKey != "" {
				return providerRuntime{ID: providerID, BaseURL: strings.TrimRight(configured.BaseURL, "/"), Adapter: adapter, APIKey: configured.APIKey}, nil
			}
		}
	}
	definition, ok := nativeProviderDefinitions[providerID]
	if !ok {
		return providerRuntime{}, fmt.Errorf("unknown provider: %s", providerID)
	}
	if definition.APIKeyEnv != "" {
		if key := strings.TrimSpace(os.Getenv(definition.APIKeyEnv)); key != "" {
			return providerRuntime{ID: providerID, BaseURL: definition.BaseURL, Adapter: definition.Adapter, APIKey: key}, nil
		}
	}
	if providerID == "anthropic" {
		return providerRuntime{}, fmt.Errorf("Anthropic subscription OAuth is not advertised for inference; configure ANTHROPIC_API_KEY")
	}
	token, err := auth.AccessToken(ctx, providerID, forceRefresh)
	if err != nil {
		return providerRuntime{}, err
	}
	return providerRuntime{ID: providerID, BaseURL: definition.BaseURL, Adapter: definition.Adapter, BearerToken: token, OAuth: true}, nil
}

func providerConfigured(cfg *Config, auth *authManager, providerID string) bool {
	_, err := resolveProviderRuntime(context.Background(), cfg, auth, providerID, false)
	return err == nil
}

func nativeCodexModels(cfg *Config, auth *authManager) []codexNamedModel {
	cache, err := readProviderModelCache(providerModelCachePath())
	if err != nil {
		cache = providerModelCache{Providers: map[string]providerModelCacheEntry{}}
	}
	return nativeCodexModelsFromCache(cfg, auth, cache)
}

func nativeCodexModelsFromCache(cfg *Config, auth *authManager, cache providerModelCache) []codexNamedModel {
	ids := make([]string, 0, len(nativeProviderDefinitions))
	for id := range nativeProviderDefinitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var models []codexNamedModel
	for _, providerID := range ids {
		if !providerConfigured(cfg, auth, providerID) {
			continue
		}
		definition := nativeProviderDefinitions[providerID]
		providerModels := definition.Models
		if cached := cache.Providers[providerID].Models; len(cached) > 0 {
			providerModels = append([]nativeProviderModel(nil), cached...)
		}
		sort.Slice(providerModels, func(i, j int) bool { return providerModels[i].ID < providerModels[j].ID })
		for _, model := range providerModels {
			reasoning := map[string]ReasoningTarget{"minimal": {}}
			for _, effort := range model.Reasoning {
				reasoning[effort] = ReasoningTarget{Effort: effort}
			}
			capability := ModelCapability{
				DisplayName: model.ID + " (" + providerID + ")", Provider: providerID, Model: model.ID,
				Enabled: true, StreamingSupport: true, ToolCallSupport: model.Tools, ImageInputSupport: model.Images,
				MaxContext: model.Context, MaxOutput: model.MaxOutput, Reasoning: reasoning,
			}
			models = append(models, codexNamedModel{
				ID: providerID + "/" + model.ID, Display: capability.DisplayName,
				Capability: capability, Route: Route{Provider: providerID, Model: model.ID, Reasoning: reasoning, MaxTokens: model.MaxOutput},
			})
		}
	}
	return models
}

func isLoginRequired(err error) bool {
	return errors.Is(err, errLoginRequired) || errors.Is(err, errCredentialNotFound)
}
