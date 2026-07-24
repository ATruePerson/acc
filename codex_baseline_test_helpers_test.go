package main

import (
	"fmt"
	"path/filepath"
)

func loadCodexSubscriptionBaseline(path, configPath string) (*codexSubscriptionBaseline, error) {
	baseline, err := readCodexBaseline(path)
	if err != nil {
		return nil, err
	}
	if err := validateCodexBaseline(baseline); err != nil {
		return nil, err
	}
	if filepath.Clean(baseline.RawConfig.Path) != filepath.Clean(configPath) {
		return nil, fmt.Errorf("baseline config path = %q, want %q", baseline.RawConfig.Path, configPath)
	}
	return baseline, nil
}
