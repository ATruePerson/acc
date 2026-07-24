package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	codexOpusID   = "gpt-5.6-sol"
	codexSonnetID = "gpt-5.6-terra"
	codexHaikuID  = "gpt-5.6-luna"
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

type codexRestoreState struct {
	ConfigExisted  bool   `json:"config_existed"`
	Config         []byte `json:"config,omitempty"`
	CatalogExisted bool   `json:"catalog_existed"`
	Catalog        []byte `json:"catalog,omitempty"`
}

const (
	accCodexRootBegin = "# BEGIN ACC CODEX OWNED"
	accCodexRootEnd   = "# END ACC CODEX OWNED"
	accCodexProvider  = "# ACC CODEX OWNED PROVIDER"
)

func configureCodexApp(configPath, catalogPath, restorePath, baseURL, model string, cfg *Config) error {
	return configureCodexAppWithAuth(configPath, catalogPath, restorePath, baseURL, model, cfg, nil)
}

func configureCodexAppWithAuth(configPath, catalogPath, restorePath, baseURL, model string, cfg *Config, auth *authManager) error {
	if !isCodexModelWithAuth(cfg, auth, model) {
		return fmt.Errorf("unknown Codex model %q", model)
	}
	if err := saveCodexRestoreState(configPath, catalogPath, restorePath); err != nil {
		return fmt.Errorf("back up Codex settings: %w", err)
	}

	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(original) > 0 {
		if _, err := writeTimestampedBackup(configPath, original); err != nil {
			return fmt.Errorf("timestamp Codex config: %w", err)
		}
	}
	configured := renderCodexConfig(string(original), catalogPath, baseURL, model)
	if err := validateCodexConfigText(configured); err != nil {
		return fmt.Errorf("generated Codex config is invalid: %w", err)
	}
	if err := atomicWriteFile(catalogPath, codexModelCatalogJSONWithAuth(cfg, auth), 0600); err != nil {
		return fmt.Errorf("write Codex model catalog: %w", err)
	}
	if err := atomicWriteFile(configPath, []byte(configured), 0600); err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	return nil
}

func restoreCodexApp(configPath, catalogPath, restorePath string) error {
	b, err := os.ReadFile(restorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no ACC Codex backup found")
		}
		return err
	}
	var state codexRestoreState
	if err := json.Unmarshal(b, &state); err != nil {
		return fmt.Errorf("read restore state: %w", err)
	}
	if current, readErr := os.ReadFile(configPath); readErr == nil {
		if _, err := writeTimestampedBackup(configPath, current); err != nil {
			return fmt.Errorf("back up current Codex config: %w", err)
		}
	}
	if state.ConfigExisted {
		if err := validateCodexConfigText(string(state.Config)); err != nil {
			return fmt.Errorf("saved Codex config is invalid: %w", err)
		}
	}
	if state.ConfigExisted {
		if err := atomicWriteFile(configPath, state.Config, 0600); err != nil {
			return err
		}
	} else if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if state.CatalogExisted {
		if err := atomicWriteFile(catalogPath, state.Catalog, 0600); err != nil {
			return err
		}
	} else if err := os.Remove(catalogPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(restorePath); err != nil {
		return err
	}
	return nil
}

func saveCodexRestoreState(configPath, catalogPath, restorePath string) error {
	if _, err := os.Stat(restorePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	state := codexRestoreState{}
	if b, err := os.ReadFile(configPath); err == nil {
		state.ConfigExisted, state.Config = true, b
	} else if !os.IsNotExist(err) {
		return err
	}
	if b, err := os.ReadFile(catalogPath); err == nil {
		state.CatalogExisted, state.Catalog = true, b
	} else if !os.IsNotExist(err) {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(restorePath, append(b, '\n'), 0600)
}

func renderCodexConfig(original, catalogPath, baseURL, model string) string {
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines)+10)
	inACCProvider := false
	inOwnedRoot := false
	inRoot := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == accCodexRootBegin {
			inOwnedRoot = true
			continue
		}
		if trimmed == accCodexRootEnd {
			inOwnedRoot = false
			continue
		}
		if inOwnedRoot || trimmed == accCodexProvider {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inRoot = false
			inACCProvider = trimmed == "[model_providers.acc]"
			if inACCProvider {
				continue
			}
		} else if inACCProvider {
			continue
		}
		if inRoot {
			key, _, ok := strings.Cut(trimmed, "=")
			if ok {
				switch strings.TrimSpace(key) {
				case "model", "model_provider", "model_catalog_json", "web_search":
					continue
				case "openai_base_url":
					if legacyOpenCodexDetected(line) {
						continue
					}
				}
			}
		}
		cleaned = append(cleaned, line)
	}

	insertAt := len(cleaned)
	for i, line := range cleaned {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			insertAt = i
			break
		}
	}
	root := []string{
		accCodexRootBegin,
		"model = " + strconv.Quote(model),
		`model_provider = "acc"`,
		"model_catalog_json = " + strconv.Quote(catalogPath),
		`web_search = "disabled"`,
		accCodexRootEnd,
		"",
	}
	cleaned = append(cleaned[:insertAt], append(root, cleaned[insertAt:]...)...)
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	cleaned = append(cleaned,
		"",
		accCodexProvider,
		"[model_providers.acc]",
		`name = "ACC"`,
		"base_url = "+strconv.Quote(strings.TrimRight(baseURL, "/")),
		`wire_api = "responses"`,
		"requires_openai_auth = false",
		"supports_websockets = false",
	)
	return strings.Join(cleaned, "\n") + "\n"
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
			if !strings.HasSuffix(trimmed, "]") || strings.Count(trimmed, "[") != strings.Count(trimmed, "]") {
				return fmt.Errorf("line %d has an invalid table header", lineNumber+1)
			}
			isArrayTable := strings.HasPrefix(trimmed, "[[")
			if seenTables[trimmed] && !isArrayTable {
				return fmt.Errorf("duplicate table %s", trimmed)
			}
			seenTables[trimmed] = true
			continue
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
	lines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inOwnedRoot, inACCProvider := false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == accCodexRootBegin {
			inOwnedRoot = true
			continue
		}
		if trimmed == accCodexRootEnd {
			inOwnedRoot = false
			continue
		}
		if inOwnedRoot || trimmed == accCodexProvider {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == "[model_providers.acc]" {
				inACCProvider = true
				continue
			}
			inACCProvider = false
		}
		if !inACCProvider {
			out = append(out, line)
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func legacyOpenCodexDetected(config string) bool {
	lower := strings.ToLower(config)
	return strings.Contains(lower, "127.0.0.1:10100") || strings.Contains(lower, "localhost:10100") || strings.Contains(lower, "opencodex")
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
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
