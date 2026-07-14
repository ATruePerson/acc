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
	codexSolID   = "openai/codex-5.6-sol"
	codexTerraID = "openai/codex-5.6-terra"
	codexLunaID  = "openai/codex-5.6-luna"
)

type codexNamedModel struct {
	ID      string
	Display string
	Family  string
}

func codexNamedModels() []codexNamedModel {
	return []codexNamedModel{
		{ID: codexSolID, Display: "Codex 5.6 Sol", Family: "opus"},
		{ID: codexTerraID, Display: "Codex 5.6 Terra", Family: "sonnet"},
		{ID: codexLunaID, Display: "Codex 5.6 Luna", Family: "haiku"},
	}
}

func codexModelCatalogEntries() []map[string]any {
	levels := []map[string]any{
		{"effort": "low", "description": "Fast responses with lighter reasoning"},
		{"effort": "medium", "description": "Balanced speed and reasoning"},
		{"effort": "high", "description": "More reasoning for difficult work"},
		{"effort": "xhigh", "description": "Extra reasoning for complex work"},
	}
	models := codexNamedModels()
	entries := make([]map[string]any, 0, len(models))
	for i, model := range models {
		entries = append(entries, map[string]any{
			"slug": model.ID, "display_name": model.Display,
			"description": "Routed through ACC", "default_reasoning_level": "high",
			"supported_reasoning_levels": levels, "shell_type": "shell_command",
			"visibility": "list", "supported_in_api": true, "priority": i + 1,
			"additional_speed_tiers": []string{}, "service_tiers": []any{},
			"availability_nux": nil, "upgrade": nil,
			"base_instructions": "You are Codex, a coding agent. Work in the user's repository, follow the supplied instructions, and use tools when needed.",
			"model_messages": map[string]any{
				"instructions_template": nil, "instructions_variables": nil, "approvals": nil,
			},
			"include_skills_usage_instructions": true,
			"supports_reasoning_summaries":      false, "default_reasoning_summary": "none",
			"support_verbosity": false, "default_verbosity": "low",
			"apply_patch_tool_type": "freeform", "web_search_tool_type": "text_and_image",
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": true, "supports_image_detail_original": true,
			"context_window": 131072, "max_context_window": 131072,
			"comp_hash": "acc", "effective_context_window_percent": 95,
			"experimental_supported_tools": []any{}, "input_modalities": []string{"text", "image"},
			"supports_search_tool": false, "use_responses_lite": false,
			"tool_mode": "code_mode_only", "multi_agent_version": "v1",
		})
	}
	return entries
}

func codexModelCatalogJSON() []byte {
	b, _ := json.MarshalIndent(map[string]any{"models": codexModelCatalogEntries()}, "", "  ")
	return append(b, '\n')
}

type codexRestoreState struct {
	ConfigExisted  bool   `json:"config_existed"`
	Config         []byte `json:"config,omitempty"`
	CatalogExisted bool   `json:"catalog_existed"`
	Catalog        []byte `json:"catalog,omitempty"`
}

func configureCodexApp(configPath, catalogPath, restorePath, baseURL, model string) error {
	if !isCodexModel(model) {
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
	if err := atomicWriteFile(catalogPath, codexModelCatalogJSON(), 0600); err != nil {
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
				case "model", "model_provider", "model_catalog_json":
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

func isCodexModel(model string) bool {
	for _, candidate := range codexNamedModels() {
		if model == candidate.ID {
			return true
		}
	}
	return false
}

// activeCodexModel reads the route selected by `acc codex`. Desktop currently
// sends its own gpt-5.4 IDs for Custom, so the proxy uses this as the source of
// truth when it receives those IDs.
func activeCodexModel() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return activeCodexModelFromConfig(filepath.Join(home, ".codex", "config.toml"))
}

func activeCodexModelFromConfig(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	var model, provider string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			break // Only the root settings choose the desktop model.
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model":
			model = unquoted
		case "model_provider":
			provider = unquoted
		}
	}
	return model, provider == "acc" && isCodexModel(model)
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
