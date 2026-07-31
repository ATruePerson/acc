package codex

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type codexAuthFingerprint struct {
	Path    string
	Existed bool
	Mode    os.FileMode
	Sum     [32]byte
}

var codexWriteFile = atomicWriteFile

func beginConfigureCodexApp(configPath, catalogPath, baselinePath, restartPath, baseURL, model string, cfg *Config, auth AuthManager) (*codexConfigTransaction, error) {
	if !isCodexModelWithAuth(cfg, auth, model) {
		return nil, fmt.Errorf("unknown Codex model %q", model)
	}
	if err := validateCodexLoopbackBaseURL(baseURL); err != nil {
		return nil, err
	}
	state, err := captureCodexMutationState(configPath, catalogPath, baselinePath, restartPath)
	if err != nil {
		return nil, err
	}
	tx := &codexConfigTransaction{state: state}
	rollback := func(err error) (*codexConfigTransaction, error) {
		invalidateCodexModelsCache()
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return nil, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return nil, err
	}

	authBefore, err := captureCodexAuthFingerprints(filepath.Dir(configPath))
	if err != nil {
		return rollback(err)
	}
	if err := backupCodexMutationState(state); err != nil {
		return rollback(fmt.Errorf("timestamp Codex state: %w", err))
	}
	_, created, err := ensureCodexSubscriptionBaseline(configPath, catalogPath, baselinePath)
	if err != nil {
		return rollback(fmt.Errorf("create subscription baseline: %w", err))
	}

	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return rollback(err)
	}
	catalog := codexModelCatalogJSONWithAuth(cfg, auth)
	if valid, detail := validateCodexCatalog(catalog); !valid {
		return rollback(fmt.Errorf("generated catalog is invalid: %s", detail))
	}
	if !catalogHasCodexModel(catalog, model) {
		return rollback(fmt.Errorf("selected model %q is missing from generated catalog", model))
	}
	configured := renderCodexACCConfig(string(original), catalogPath, baseURL, model, "")
	if err := validateCodexConfigText(configured); err != nil {
		return rollback(fmt.Errorf("generated Codex config is invalid: %w", err))
	}
	routing := inspectCodexRouting(configured)
	if routing.Mode != "ACC" || routing.Provider != "acc" || filepath.Clean(resolveCodexPath(routing.Catalog, configPath)) != filepath.Clean(catalogPath) {
		return rollback(fmt.Errorf("generated Codex config does not point exclusively to ACC"))
	}

	catalogSnapshot, _ := captureCodexFile(catalogPath)
	configSnapshot, _ := captureCodexFile(configPath)
	restartRequired := false
	if fileSnapshotDiffers(catalogSnapshot, catalog, true) {
		if err := codexWriteFile(catalogPath, catalog, 0600); err != nil {
			return rollback(fmt.Errorf("write Codex model catalog: %w", err))
		}
		restartRequired = true
	}
	if fileSnapshotDiffers(configSnapshot, []byte(configured), true) {
		if err := codexWriteFile(configPath, []byte(configured), 0600); err != nil {
			return rollback(fmt.Errorf("write Codex config: %w", err))
		}
		restartRequired = true
	}
	if restartRequired {
		if err := markCodexRestartRequired(restartPath); err != nil {
			return rollback(err)
		}
	}

	writtenConfig, err := os.ReadFile(configPath)
	if err != nil {
		return rollback(err)
	}
	writtenCatalog, err := os.ReadFile(catalogPath)
	if err != nil {
		return rollback(err)
	}
	if inspectCodexRouting(string(writtenConfig)).Mode != "ACC" || !catalogHasCodexModel(writtenCatalog, model) {
		return rollback(fmt.Errorf("post-write Codex verification failed"))
	}
	authUnchanged, err := codexAuthFingerprintsUnchanged(authBefore)
	if err != nil {
		return rollback(err)
	}
	if !authUnchanged {
		return rollback(fmt.Errorf("Codex authentication files changed unexpectedly"))
	}
	if restartRequired {
		invalidateCodexModelsCache()
	}
	tx.result = codexConfigureResult{BaselineCreated: created, RestartRequired: restartRequired, AuthUnchanged: true}
	return tx, nil
}

func restoreCodexAppDetailed(configPath, catalogPath, baselinePath, restartPath string) (codexRestoreResult, error) {
	currentConfig, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return codexRestoreResult{}, err
	}
	paths := []string{configPath, catalogPath, baselinePath, restartPath}
	if active := activeCodexCatalogPath(string(currentConfig), configPath); active != "" {
		paths = append(paths, active)
	}
	if baseline, readErr := readCodexBaseline(baselinePath); readErr == nil {
		for _, catalog := range baseline.Catalogs {
			paths = append(paths, catalog.Path)
		}
	}
	paths = uniqueCleanPaths(paths)
	state, err := captureCodexMutationState(paths...)
	if err != nil {
		return codexRestoreResult{}, err
	}
	rollback := func(result codexRestoreResult, err error) (codexRestoreResult, error) {
		if rollbackErr := rollbackCodexMutationState(state); rollbackErr != nil {
			return result, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return result, err
	}
	if err := backupCodexMutationState(state); err != nil {
		return rollback(codexRestoreResult{}, fmt.Errorf("timestamp Codex state: %w", err))
	}
	authBefore, err := captureCodexAuthFingerprints(filepath.Dir(configPath))
	if err != nil {
		return rollback(codexRestoreResult{}, err)
	}
	baseline, recovery, err := ensureCodexSubscriptionBaseline(configPath, catalogPath, baselinePath)
	if err != nil {
		return rollback(codexRestoreResult{}, err)
	}
	if err := validateCodexBaseline(baseline); err != nil {
		return rollback(codexRestoreResult{}, err)
	}

	result := codexRestoreResult{RecoveryMode: recovery, AuthUnchanged: true}
	configSnapshot, _ := captureCodexFile(configPath)
	targetExists := baseline.SanitizedConfig.Existed
	targetConfig := baseline.SanitizedConfig.Data
	if fileSnapshotDiffers(configSnapshot, targetConfig, targetExists) {
		if targetExists {
			mode := os.FileMode(baseline.SanitizedConfig.Mode)
			if mode == 0 {
				mode = 0600
			}
			if err := codexWriteFile(configPath, targetConfig, mode); err != nil {
				return rollback(result, err)
			}
		} else if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return rollback(result, err)
		}
		result.RestartRequired = true
	}

	baselineCatalogs := map[string]codexFileSnapshot{}
	for _, snapshot := range baseline.Catalogs {
		baselineCatalogs[filepath.Clean(snapshot.Path)] = snapshot
	}
	candidateCatalogs := []string{catalogPath}
	if active := activeCodexCatalogPath(string(currentConfig), configPath); active != "" {
		candidateCatalogs = append(candidateCatalogs, active)
	}
	for path, snapshot := range baselineCatalogs {
		candidateCatalogs = append(candidateCatalogs, path)
		current, _ := captureCodexFile(path)
		if snapshot.Existed && snapshot.Preserve {
			if fileSnapshotDiffers(current, snapshot.Data, true) {
				mode := os.FileMode(snapshot.Mode)
				if mode == 0 {
					mode = 0600
				}
				if err := codexWriteFile(path, snapshot.Data, mode); err != nil {
					return rollback(result, err)
				}
				result.RestartRequired = true
			}
		} else if current.Existed && knownManagedCodexCatalog(path, catalogPath) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return rollback(result, err)
			}
			result.DeletedCatalogs = append(result.DeletedCatalogs, path)
			result.RestartRequired = true
		}
	}
	for _, path := range uniqueCleanPaths(candidateCatalogs) {
		if snapshot, ok := baselineCatalogs[path]; ok && snapshot.Existed && snapshot.Preserve {
			continue
		}
		if path == filepath.Clean(catalogPath) {
			if _, ok := baselineCatalogs[path]; ok {
				continue
			}
		}
		current, _ := captureCodexFile(path)
		if current.Existed && knownManagedCodexCatalog(path, catalogPath) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return rollback(result, err)
			}
			result.DeletedCatalogs = append(result.DeletedCatalogs, path)
			result.RestartRequired = true
		}
	}

	finalConfig, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return rollback(result, err)
	}
	if err := validateSubscriptionCodexConfig(string(finalConfig)); err != nil {
		return rollback(result, fmt.Errorf("restored subscription config is invalid: %w", err))
	}
	if result.RestartRequired {
		if err := markCodexRestartRequired(restartPath); err != nil {
			return rollback(result, err)
		}
	}
	authUnchanged, err := codexAuthFingerprintsUnchanged(authBefore)
	if err != nil {
		return rollback(result, err)
	}
	if !authUnchanged {
		return rollback(result, fmt.Errorf("Codex authentication files changed unexpectedly"))
	}
	if result.RestartRequired {
		invalidateCodexModelsCache()
	}
	sort.Strings(result.DeletedCatalogs)
	return result, nil
}

func markCodexRestartRequired(path string) error {
	return codexWriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0600)
}

func clearCodexRestartRequired(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func captureCodexAuthFingerprints(codexDir string) ([]codexAuthFingerprint, error) {
	entries, err := os.ReadDir(codexDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var fingerprints []codexAuthFingerprint
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.Contains(name, "auth") && !strings.Contains(name, "token") && !strings.Contains(name, "credential") && !strings.Contains(name, "login") && !strings.Contains(name, "cookie") {
			continue
		}
		path := filepath.Join(codexDir, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			if err != nil {
				return nil, err
			}
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fingerprints = append(fingerprints, codexAuthFingerprint{Path: path, Existed: true, Mode: info.Mode().Perm(), Sum: sha256.Sum256(body)})
	}
	sort.Slice(fingerprints, func(i, j int) bool { return fingerprints[i].Path < fingerprints[j].Path })
	return fingerprints, nil
}

func codexAuthFingerprintsUnchanged(before []codexAuthFingerprint) (bool, error) {
	for _, fingerprint := range before {
		info, err := os.Stat(fingerprint.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if info.Mode().Perm() != fingerprint.Mode {
			return false, nil
		}
		body, err := os.ReadFile(fingerprint.Path)
		if err != nil {
			return false, err
		}
		if sha256.Sum256(body) != fingerprint.Sum {
			return false, nil
		}
	}
	return true, nil
}
