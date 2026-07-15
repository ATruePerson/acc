package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	codexOpusID   = "opus"
	codexSonnetID = "sonnet"
	codexHaikuID  = "haiku"
)

type codexNamedModel struct {
	ID         string
	Display    string
	Capability ModelCapability
	Route      Route
}

func codexNamedModels(cfg *Config) []codexNamedModel {
	models := make([]codexNamedModel, 0, len(cfg.Models))
	for _, id := range enabledModelIDs(cfg) {
		capability := cfg.Models[id]
		if capability.CatalogVisible != nil && !*capability.CatalogVisible {
			continue
		}
		route, err := resolveCapabilityRoute(cfg, id, capability)
		if err != nil {
			continue
		}
		display := capability.DisplayName
		if display == "" {
			display = id
		}
		models = append(models, codexNamedModel{ID: id, Display: display, Capability: capability, Route: route})
	}
	return models
}

func codexModelCatalogEntries(cfg *Config) []map[string]any {
	models := codexNamedModels(cfg)
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
		if model.Capability.ImageInputSupport {
			modalities = append(modalities, "image")
		}
		entries = append(entries, map[string]any{
			"slug": model.ID, "display_name": model.Display,
			"description": fmt.Sprintf("Kabir's Second Brain via %s/%s", model.Route.Provider, model.Route.Model), "default_reasoning_level": defaultEffort,
			"supported_reasoning_levels": levels, "shell_type": "shell_command",
			"visibility": "list", "supported_in_api": true, "priority": i + 1,
			"additional_speed_tiers": []string{}, "service_tiers": []any{},
			"availability_nux": nil, "upgrade": nil,
			"base_instructions": accPersona(model.Route.Provider, model.Route.Model),
			"model_messages": map[string]any{
				"instructions_template": nil, "instructions_variables": nil, "approvals": nil,
			},
			"include_skills_usage_instructions": true,
			"supports_reasoning_summaries":      false, "default_reasoning_summary": "none",
			"support_verbosity": false, "default_verbosity": "low",
			"apply_patch_tool_type":        "freeform",
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": model.Capability.ToolCallSupport, "supports_image_detail_original": model.Capability.ImageInputSupport,
			"context_window": model.Capability.MaxContext, "max_context_window": model.Capability.MaxContext,
			"max_output_tokens": model.Capability.MaxOutput,
			"comp_hash":         "acc", "effective_context_window_percent": 95,
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
	b, _ := json.MarshalIndent(map[string]any{"models": codexModelCatalogEntries(cfg)}, "", "  ")
	return append(b, '\n')
}

type codexRestoreState struct {
	ConfigExisted  bool   `json:"config_existed"`
	Config         []byte `json:"config,omitempty"`
	CatalogExisted bool   `json:"catalog_existed"`
	Catalog        []byte `json:"catalog,omitempty"`
}

func configureCodexApp(configPath, catalogPath, restorePath, baseURL, model string, cfg *Config) error {
	if !isCodexModel(cfg, model) {
		return fmt.Errorf("unknown Codex model %q", model)
	}
	if err := saveCodexRestoreState(configPath, catalogPath, restorePath); err != nil {
		return fmt.Errorf("back up Codex settings: %w", err)
	}

	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	configured := renderCodexConfig(string(original), catalogPath, baseURL, model)
	if err := atomicWriteFile(catalogPath, codexModelCatalogJSON(cfg), 0600); err != nil {
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
	inRoot := true
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
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
		"model = " + strconv.Quote(model),
		`model_provider = "acc"`,
		"model_catalog_json = " + strconv.Quote(catalogPath),
		`web_search = "disabled"`,
		"",
	}
	cleaned = append(cleaned[:insertAt], append(root, cleaned[insertAt:]...)...)
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	cleaned = append(cleaned,
		"",
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
	for _, candidate := range codexNamedModels(cfg) {
		if model == candidate.ID {
			return true
		}
	}
	return false
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
