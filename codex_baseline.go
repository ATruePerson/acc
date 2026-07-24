package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const codexBaselineVersion = 2

type codexFileSnapshot struct {
	Path     string `json:"path"`
	Existed  bool   `json:"existed"`
	Preserve bool   `json:"preserve,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

type codexSubscriptionBaseline struct {
	Version         int                 `json:"version"`
	CreatedAt       time.Time           `json:"created_at"`
	RawConfig       codexFileSnapshot   `json:"raw_config"`
	Catalogs        []codexFileSnapshot `json:"catalogs,omitempty"`
	SanitizedConfig codexFileSnapshot   `json:"sanitized_config"`
}

type codexConfigureResult struct {
	BaselineCreated bool
	RestartRequired bool
	AuthUnchanged   bool
}

type codexRestoreResult struct {
	RecoveryMode    bool
	RestartRequired bool
	AuthUnchanged   bool
	DeletedCatalogs []string
}

type codexConfigTransaction struct {
	state     codexMutationState
	result    codexConfigureResult
	committed bool
}

type codexMutationState struct {
	Files []codexFileSnapshot
}

func captureCodexFile(path string) (codexFileSnapshot, error) {
	snapshot := codexFileSnapshot{Path: filepath.Clean(path)}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return snapshot, err
	}
	if !info.Mode().IsRegular() {
		return snapshot, fmt.Errorf("refusing to snapshot non-regular file %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	snapshot.Existed = true
	snapshot.Preserve = true
	snapshot.Mode = uint32(info.Mode().Perm())
	snapshot.Data = body
	return snapshot, nil
}

func captureCodexMutationState(paths ...string) (codexMutationState, error) {
	seen := map[string]bool{}
	state := codexMutationState{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		snapshot, err := captureCodexFile(clean)
		if err != nil {
			return codexMutationState{}, err
		}
		state.Files = append(state.Files, snapshot)
	}
	return state, nil
}

func restoreCodexFile(snapshot codexFileSnapshot) error {
	if snapshot.Existed {
		mode := os.FileMode(snapshot.Mode)
		if mode == 0 {
			mode = 0600
		}
		return atomicWriteFile(snapshot.Path, snapshot.Data, mode)
	}
	if err := os.Remove(snapshot.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func rollbackCodexMutationState(state codexMutationState) error {
	var first error
	for i := len(state.Files) - 1; i >= 0; i-- {
		if err := restoreCodexFile(state.Files[i]); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func backupCodexMutationState(state codexMutationState) error {
	for _, snapshot := range state.Files {
		if !snapshot.Existed {
			continue
		}
		if _, err := writeTimestampedBackup(snapshot.Path, snapshot.Data); err != nil {
			return err
		}
	}
	return nil
}

func (tx *codexConfigTransaction) Commit() codexConfigureResult {
	tx.committed = true
	return tx.result
}

func (tx *codexConfigTransaction) Rollback() error {
	if tx == nil || tx.committed {
		return nil
	}
	tx.committed = true
	return rollbackCodexMutationState(tx.state)
}

func readCodexBaseline(path string) (*codexSubscriptionBaseline, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var baseline codexSubscriptionBaseline
	if err := json.Unmarshal(body, &baseline); err != nil {
		return nil, fmt.Errorf("decode subscription baseline: %w", err)
	}
	return &baseline, nil
}

func validateCodexBaseline(baseline *codexSubscriptionBaseline) error {
	if baseline == nil || baseline.Version != codexBaselineVersion {
		return fmt.Errorf("unsupported or missing baseline version")
	}
	if baseline.RawConfig.Path == "" || baseline.SanitizedConfig.Path == "" {
		return fmt.Errorf("baseline is missing config snapshots")
	}
	if baseline.RawConfig.Path != baseline.SanitizedConfig.Path {
		return fmt.Errorf("baseline config paths disagree")
	}
	if baseline.SanitizedConfig.Existed {
		if err := validateCodexConfigText(string(baseline.SanitizedConfig.Data)); err != nil {
			return fmt.Errorf("sanitized baseline is invalid: %w", err)
		}
		if err := validateSubscriptionCodexConfig(string(baseline.SanitizedConfig.Data)); err != nil {
			return fmt.Errorf("sanitized baseline still has custom routing: %w", err)
		}
	}
	return nil
}

func codexBaselineStatus(path, currentConfig string) string {
	baseline, err := readCodexBaseline(path)
	if err == nil && validateCodexBaseline(baseline) == nil {
		return "Valid"
	}
	if validateCodexConfigText(currentConfig) == nil {
		return "Recoverable"
	}
	return "Missing"
}

func ensureCodexSubscriptionBaseline(configPath, catalogPath, baselinePath string) (*codexSubscriptionBaseline, bool, error) {
	if _, statErr := os.Stat(baselinePath); statErr == nil {
		if baseline, readErr := readCodexBaseline(baselinePath); readErr == nil && validateCodexBaseline(baseline) == nil {
			return baseline, false, nil
		}
	} else if !os.IsNotExist(statErr) {
		return nil, false, statErr
	}

	rawConfig, err := captureCodexFile(configPath)
	if err != nil {
		return nil, false, err
	}
	sanitized := sanitizeCodexSubscriptionConfig(string(rawConfig.Data))
	if err := validateCodexConfigText(sanitized); err != nil {
		return nil, false, fmt.Errorf("sanitize subscription config: %w", err)
	}
	if err := validateSubscriptionCodexConfig(sanitized); err != nil {
		return nil, false, fmt.Errorf("sanitize subscription routing: %w", err)
	}

	catalogPaths := []string{catalogPath}
	if active := activeCodexCatalogPath(string(rawConfig.Data), configPath); active != "" {
		catalogPaths = append(catalogPaths, active)
	}
	catalogPaths = uniqueCleanPaths(catalogPaths)
	catalogs := make([]codexFileSnapshot, 0, len(catalogPaths))
	routing := inspectCodexRouting(string(rawConfig.Data))
	activeCatalog := activeCodexCatalogPath(string(rawConfig.Data), configPath)
	for _, path := range catalogPaths {
		snapshot, err := captureCodexFile(path)
		if err != nil {
			return nil, false, err
		}
		if snapshot.Existed && filepath.Clean(path) == filepath.Clean(activeCatalog) && (routing.Mode == "ACC" || routing.Mode == "OpenCodex") && knownManagedCodexCatalog(path, catalogPath) {
			snapshot.Preserve = false
		}
		catalogs = append(catalogs, snapshot)
	}

	baseline := &codexSubscriptionBaseline{
		Version:   codexBaselineVersion,
		CreatedAt: time.Now().UTC(),
		RawConfig: rawConfig,
		Catalogs:  catalogs,
		SanitizedConfig: codexFileSnapshot{
			Path:     rawConfig.Path,
			Existed:  rawConfig.Existed,
			Preserve: rawConfig.Preserve,
			Mode:     rawConfig.Mode,
			Data:     []byte(sanitized),
		},
	}
	body, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return nil, false, err
	}
	if err := codexWriteFile(baselinePath, append(body, '\n'), 0600); err != nil {
		return nil, false, err
	}
	return baseline, true, nil
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

func fileSnapshotDiffers(snapshot codexFileSnapshot, target []byte, targetExists bool) bool {
	if snapshot.Existed != targetExists {
		return true
	}
	if !targetExists {
		return false
	}
	return !stringSlicesEqual(snapshot.Data, target)
}

func stringSlicesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
