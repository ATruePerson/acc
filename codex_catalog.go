package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type codexNamedModel struct {
	ID         string
	Display    string
	Capability ModelCapability
	Route      Route
}

func codexNamedModels(cfg *Config) []codexNamedModel {
	return codexNamedModelsWithAuth(cfg, nil)
}

func codexNamedModelsWithAuth(cfg *Config, auth *authManager) []codexNamedModel {
	byID := make(map[string]codexNamedModel, len(cfg.Models))
	for _, id := range enabledModelIDs(cfg) {
		capability := cfg.Models[id]
		if capability.CatalogVisible != nil && !*capability.CatalogVisible {
			continue
		}
		route, err := resolveCapabilityRoute(cfg, id, capability)
		if err != nil {
			continue
		}
		realID := route.Provider + "/" + strings.TrimPrefix(route.Model, "/")
		display := capability.DisplayName
		if display == "" {
			display = route.Model + " (" + route.Provider + ")"
		}
		candidate := codexNamedModel{ID: realID, Display: display, Capability: capability, Route: route}
		if existing, ok := byID[realID]; ok && existing.Capability.CatalogPriority > 0 &&
			(candidate.Capability.CatalogPriority == 0 || existing.Capability.CatalogPriority <= candidate.Capability.CatalogPriority) {
			continue
		}
		byID[realID] = candidate
	}
	for _, candidate := range nativeCodexModels(cfg, auth) {
		if _, exists := byID[candidate.ID]; !exists {
			byID[candidate.ID] = candidate
		}
	}
	models := make([]codexNamedModel, 0, len(byID))
	for _, model := range byID {
		models = append(models, model)
	}
	sort.SliceStable(models, func(i, j int) bool {
		left, right := models[i].Capability.CatalogPriority, models[j].Capability.CatalogPriority
		if left == 0 {
			left = int(^uint(0) >> 1)
		}
		if right == 0 {
			right = int(^uint(0) >> 1)
		}
		if left != right {
			return left < right
		}
		return models[i].ID < models[j].ID
	})
	return models
}

func codexModelCatalogEntries(cfg *Config) []map[string]any {
	return codexModelCatalogEntriesWithAuth(cfg, nil)
}

func codexModelCatalogEntriesWithAuth(cfg *Config, auth *authManager) []map[string]any {
	models := codexNamedModelsWithAuth(cfg, auth)
	entries := make([]map[string]any, 0, len(models))
	for i, model := range models {
		levels := make([]map[string]any, 0, len(model.Capability.Reasoning))
		for _, effort := range supportedEfforts(model.Capability) {
			levels = append(levels, map[string]any{"effort": effort, "description": reasoningDescription(effort)})
		}
		defaultEffort := "minimal"
		for _, candidate := range []string{"max", "xhigh", "high", "medium", "low", "minimal"} {
			if _, ok := model.Capability.Reasoning[candidate]; ok {
				defaultEffort = candidate
				break
			}
		}
		modalities := []string{"text"}
		supportsImages := model.Capability.ImageInputSupport
		if supportsImages {
			modalities = append(modalities, "image")
		}
		description := model.Capability.Description
		if description == "" {
			description = fmt.Sprintf("Direct Codex route to %s/%s", model.Route.Provider, model.Route.Model)
		}
		effectiveContextPercent := 95
		if model.Capability.MaxContext > 0 && model.Capability.MaxOutput > 0 && model.Capability.MaxOutput < model.Capability.MaxContext {
			effectiveContextPercent = (model.Capability.MaxContext - model.Capability.MaxOutput) * 100 / model.Capability.MaxContext
		}
		entries = append(entries, map[string]any{
			"slug": model.ID, "display_name": model.Display,
			"description": description, "default_reasoning_level": defaultEffort,
			"supported_reasoning_levels": levels, "shell_type": "shell_command",
			"visibility": "list", "supported_in_api": true, "priority": i + 1,
			"additional_speed_tiers": []string{}, "service_tiers": []any{},
			"availability_nux": nil, "upgrade": nil,
			"base_instructions": "",
			"model_messages": map[string]any{"instructions_template": nil, "instructions_variables": nil, "approvals": nil},
			"include_skills_usage_instructions": true,
			"supports_reasoning_summaries": false, "default_reasoning_summary": "none",
			"support_verbosity": false, "default_verbosity": "low",
			"apply_patch_tool_type": "freeform",
			"truncation_policy": map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": model.Capability.ToolCallSupport,
			"supports_image_detail_original": supportsImages,
			"context_window": model.Capability.MaxContext,
			"max_context_window": model.Capability.MaxContext,
			"max_output_tokens": model.Capability.MaxOutput,
			"comp_hash": "acc", "effective_context_window_percent": effectiveContextPercent,
			"experimental_supported_tools": []any{}, "input_modalities": modalities,
			"supports_search_tool": false, "use_responses_lite": false,
			"tool_mode": "code_mode_only", "multi_agent_version": "v1",
		})
	}
	return entries
}

func reasoningDescription(effort string) string {
	switch effort {
	case "minimal":
		return "No optional provider reasoning effort"
	case "low":
		return "Fast responses with lighter reasoning"
	case "medium":
		return "Balanced speed and reasoning"
	case "high":
		return "More reasoning for difficult work"
	case "xhigh":
		return "Extra reasoning for complex work"
	case "max":
		return "Maximum provider-supported reasoning"
	default:
		return effort
	}
}

func codexModelCatalogJSON(cfg *Config) []byte {
	return codexModelCatalogJSONWithAuth(cfg, nil)
}

func codexModelCatalogJSONWithAuth(cfg *Config, auth *authManager) []byte {
	b, _ := json.MarshalIndent(map[string]any{"models": codexModelCatalogEntriesWithAuth(cfg, auth)}, "", "  ")
	return append(b, '\n')
}
