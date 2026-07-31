package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"embed"
)

const (
	providersFileName = "providers.json"
	claudeConfigRel   = "claude/config.json"
	codexConfigRel    = "codex/config.json"
)

//go:embed providers.json
var defaultProvidersJSON string

//go:embed claude/config.json
var defaultClaudeConfigJSON string

//go:embed codex/config.json
var defaultCodexConfigJSON string

//go:embed claude/system_prompts/*
var embeddedClaudePrompts embed.FS

// configRootFromPath resolves the ACC config root from a -config path that may
// be a directory, providers.json, or a legacy monolithic config.json.
func configRootFromPath(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return path, nil
	}
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	switch base {
	case providersFileName:
		return dir, nil
	case "config.json":
		// Legacy single file, or claude/codex nested config.json.
		if filepath.Base(dir) == "claude" || filepath.Base(dir) == "codex" {
			return filepath.Dir(dir), nil
		}
		return dir, nil
	default:
		return dir, nil
	}
}

func splitLayoutExists(root string) bool {
	for _, rel := range []string{providersFileName, claudeConfigRel, codexConfigRel} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return false
		}
	}
	return true
}

func legacyConfigPath(root string) string {
	return filepath.Join(root, "config.json")
}

// loadConfig loads either the split layout (providers + claude + codex) or a
// legacy monolithic config.json. path may be the config root directory,
// providers.json, or legacy config.json.
func loadConfig(path string) (*Config, error) {
	root, err := configRootFromPath(path)
	if err != nil {
		// If path does not exist yet, still try treating a missing providers.json
		// parent as root when the caller passed a concrete file path.
		if os.IsNotExist(err) && strings.HasSuffix(path, providersFileName) {
			root = filepath.Dir(path)
		} else if os.IsNotExist(err) && filepath.Base(path) == "config.json" {
			root = filepath.Dir(path)
		} else {
			return nil, err
		}
	}

	if splitLayoutExists(root) {
		return loadSplitConfig(root)
	}
	legacy := path
	if filepath.Base(path) != "config.json" {
		legacy = legacyConfigPath(root)
	} else if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		legacy = legacyConfigPath(root)
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil, fmt.Errorf("config: split layout missing under %s and legacy %s not found", root, legacy)
	}
	return loadLegacyConfig(legacy)
}

func loadSplitConfig(root string) (*Config, error) {
	providersPath := filepath.Join(root, providersFileName)
	claudePath := filepath.Join(root, claudeConfigRel)
	codexPath := filepath.Join(root, codexConfigRel)

	var c Config
	if err := decodeConfigFile(providersPath, &c); err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}
	var claude partialClaudeConfig
	if err := decodeConfigFile(claudePath, &claude); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	var codex partialCodexConfig
	if err := decodeConfigFile(codexPath, &codex); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}

	c.AliasRoutes = claude.AliasRoutes
	c.Models = codex.Models
	if c.Routes == nil {
		c.Routes = map[string]Route{}
	}
	if c.Port == 0 {
		c.Port = 8787
	}

	setPersonaFilePath(resolvePersonaFile(root))

	resolved, err := resolveSystemPrepend(root, c.SystemPrepend)
	if err != nil {
		return nil, err
	}
	c.SystemPrepend = resolved

	claudeDir := filepath.Join(root, "claude")
	for k, r := range c.AliasRoutes {
		resolved, err := resolveSystemPrepend(claudeDir, r.SystemPrepend)
		if err != nil {
			return nil, fmt.Errorf("alias route %q: %w", k, err)
		}
		r.SystemPrepend = resolved
		c.AliasRoutes[k] = r
	}
	for k, r := range c.Routes {
		r.SystemPrepend = ""
		c.Routes[k] = r
	}
	for k, r := range c.Aliases {
		r.SystemPrepend = ""
		c.Aliases[k] = r
	}
	return &c, nil
}

type partialClaudeConfig struct {
	AliasRoutes map[string]Route `json:"alias_routes"`
}

type partialCodexConfig struct {
	Models map[string]ModelCapability `json:"models"`
}

func loadLegacyConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = expandEnv(b)
	if err := validateNoLegacyRoutingKeys(b); err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Port == 0 {
		c.Port = 8787
	}

	baseDir := filepath.Dir(path)
	setPersonaFilePath(resolvePersonaFile(baseDir))

	resolved, err := resolveSystemPrepend(baseDir, c.SystemPrepend)
	if err != nil {
		return nil, err
	}
	c.SystemPrepend = resolved

	for k, r := range c.Routes {
		r.SystemPrepend = ""
		c.Routes[k] = r
	}
	for k, r := range c.AliasRoutes {
		resolved, err := resolveSystemPrepend(baseDir, r.SystemPrepend)
		if err != nil {
			return nil, fmt.Errorf("alias route %q: %w", k, err)
		}
		r.SystemPrepend = resolved
		c.AliasRoutes[k] = r
	}
	for k, r := range c.Aliases {
		r.SystemPrepend = ""
		c.Aliases[k] = r
	}
	return &c, nil
}

func decodeConfigFile(path string, dest any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b = expandEnv(b)
	if err := validateNoLegacyRoutingKeys(b); err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

// configSourcesModTime returns the newest modtime among split (or legacy) files.
func configSourcesModTime(path string) (int64, error) {
	root, err := configRootFromPath(path)
	if err != nil {
		fi, err2 := os.Stat(path)
		if err2 != nil {
			return 0, err
		}
		return fi.ModTime().UnixNano(), nil
	}
	if splitLayoutExists(root) {
		var newest time.Time
		for _, rel := range []string{providersFileName, claudeConfigRel, codexConfigRel} {
			fi, err := os.Stat(filepath.Join(root, rel))
			if err != nil {
				return 0, err
			}
			if fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
		return newest.UnixNano(), nil
	}
	fi, err := os.Stat(legacyConfigPath(root))
	if err != nil {
		fi, err = os.Stat(path)
		if err != nil {
			return 0, err
		}
	}
	return fi.ModTime().UnixNano(), nil
}

// mergedDefaultConfigJSON builds one JSON document from the embedded split
// defaults for tests and tooling that still expect a single blob.
func mergedDefaultConfigJSON() string {
	var c Config
	if err := json.Unmarshal([]byte(defaultProvidersJSON), &c); err != nil {
		panic(err)
	}
	var claude partialClaudeConfig
	if err := json.Unmarshal([]byte(defaultClaudeConfigJSON), &claude); err != nil {
		panic(err)
	}
	var codex partialCodexConfig
	if err := json.Unmarshal([]byte(defaultCodexConfigJSON), &codex); err != nil {
		panic(err)
	}
	c.AliasRoutes = claude.AliasRoutes
	c.Models = codex.Models
	if c.Routes == nil {
		c.Routes = map[string]Route{}
	}
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// writeDefaultSplitConfig writes the embedded split layout under root.
func writeDefaultSplitConfig(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "claude", "system_prompts"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "codex"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "system_prompts"), 0o755); err != nil {
		return err
	}
	writes := map[string]string{
		filepath.Join(root, providersFileName): defaultProvidersJSON,
		filepath.Join(root, claudeConfigRel):   defaultClaudeConfigJSON,
		filepath.Join(root, codexConfigRel):    defaultCodexConfigJSON,
	}
	for path, body := range writes {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := fs.WalkDir(embeddedClaudePrompts, "claude/system_prompts", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := embeddedClaudePrompts.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("claude/system_prompts", path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "claude", "system_prompts", rel), b, 0o644)
	}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "system_prompts", "persona.md"), []byte(embeddedPersonaMarkdown), 0o644)
}

// hasAnyConfig reports whether root already has split or legacy config.
func hasAnyConfig(root string) bool {
	if splitLayoutExists(root) {
		return true
	}
	_, err := os.Stat(legacyConfigPath(root))
	return err == nil
}
