package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	return renderCodexACCConfig(original, catalogPath, baseURL, model)
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
