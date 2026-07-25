package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	codexDefaultModel = "nvidia/z-ai/glm-5.2"
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
	_ = auth
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
		candidate := codexNamedModel{
			ID: realID, Display: route.Model + " (" + route.Provider + ")",
			Capability: capability, Route: route,
		}
		if existing, ok := byID[realID]; ok && existing.Capability.CatalogPriority > 0 &&
			(candidate.Capability.CatalogPriority == 0 || existing.Capability.CatalogPriority <= candidate.Capability.CatalogPriority) {
			continue
		}
		byID[realID] = candidate
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
			levels = append(levels, map[string]any{
				"effort":      effort,
				"description": reasoningDescription(effort),
			})
		}
		defaultEffort := "minimal"
		for _, candidate := range []string{"max", "xhigh", "high", "medium", "low", "minimal"} {
			if _, ok := model.Capability.Reasoning[candidate]; ok {
				defaultEffort = candidate
				break
			}
		}
		modalities := []string{"text"}
		supportsImages := model.Capability.ImageInputSupport || model.Capability.ImageModel != "" || len(model.Capability.ImageFallbackModels) > 0
		if supportsImages {
			modalities = append(modalities, "image")
		}
		description := model.Capability.Description
		if description == "" {
			description = fmt.Sprintf("Kabir's Second Brain via %s/%s", model.Route.Provider, model.Route.Model)
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
			"base_instructions": accPersonaForRuntime(model.Route.Provider, model.Route.Model, personaRuntimeCodex),
			"model_messages": map[string]any{
				"instructions_template": nil, "instructions_variables": nil, "approvals": nil,
			},
			"include_skills_usage_instructions": true,
			"supports_reasoning_summaries":      false, "default_reasoning_summary": "none",
			"support_verbosity": false, "default_verbosity": "low",
			"apply_patch_tool_type":        "freeform",
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": model.Capability.ToolCallSupport, "supports_image_detail_original": supportsImages,
			"context_window": model.Capability.MaxContext, "max_context_window": model.Capability.MaxContext,
			"max_output_tokens": model.Capability.MaxOutput,
			"comp_hash":         "acc", "effective_context_window_percent": effectiveContextPercent,
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

const (
	accCodexRootBegin = "# BEGIN ACC CODEX OWNED"
	accCodexRootEnd   = "# END ACC CODEX OWNED"
	accCodexProvider  = "# ACC CODEX OWNED PROVIDER"
)

func codexRestartPathForBaseline(baselinePath string) string {
	return filepath.Join(filepath.Dir(baselinePath), "codex-restart-required")
}

func configureCodexApp(configPath, catalogPath, restorePath, baseURL, model string, cfg *Config) error {
	return configureCodexAppWithAuth(configPath, catalogPath, restorePath, baseURL, model, cfg, nil)
}

func configureCodexAppWithAuth(configPath, catalogPath, restorePath, baseURL, model string, cfg *Config, auth *authManager) error {
	tx, err := beginConfigureCodexApp(configPath, catalogPath, restorePath, codexRestartPathForBaseline(restorePath), baseURL, model, cfg, auth)
	if err != nil {
		return err
	}
	tx.Commit()
	return nil
}

func restoreCodexApp(configPath, catalogPath, restorePath string) error {
	_, err := restoreCodexAppDetailed(configPath, catalogPath, restorePath, codexRestartPathForBaseline(restorePath))
	return err
}

func saveCodexRestoreState(configPath, catalogPath, restorePath string) error {
	_, _, err := ensureCodexSubscriptionBaseline(configPath, catalogPath, restorePath)
	return err
}

func renderCodexConfig(original, catalogPath, baseURL, model string) string {
	return renderCodexACCConfig(original, catalogPath, baseURL, model, "")
}

func isCodexModel(cfg *Config, model string) bool {
	return isCodexModelWithAuth(cfg, nil, model)
}

func isCodexModelWithAuth(cfg *Config, auth *authManager, model string) bool {
	for _, candidate := range codexNamedModelsWithAuth(cfg, auth) {
		if model == candidate.ID {
			return true
		}
	}
	return false
}

func validateCodexConfigText(text string) error {
	seenTables := map[string]bool{}
	for lineNumber, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			header := strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
			if !strings.HasSuffix(header, "]") || strings.Count(header, "[") != strings.Count(header, "]") {
				return fmt.Errorf("line %d has an invalid table header", lineNumber+1)
			}
			isArrayTable := strings.HasPrefix(header, "[[")
			if seenTables[header] && !isArrayTable {
				return fmt.Errorf("duplicate table %s", header)
			}
			seenTables[header] = true
		}
	}
	return nil
}

func writeTimestampedBackup(path string, data []byte) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backup := path + ".acc-backup-" + stamp
	if err := atomicWriteFile(backup, data, 0600); err != nil {
		return "", err
	}
	return backup, nil
}

func removeACCFromCodexConfig(original string) string {
	return sanitizeCodexSubscriptionConfig(original)
}

func legacyOpenCodexDetected(config string) bool {
	return inspectCodexRouting(config).ActiveOpenCodex
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".acc-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
