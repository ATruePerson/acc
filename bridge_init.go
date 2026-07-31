package main

import (
	"github.com/ATruePerson/acc/claude"
	"github.com/ATruePerson/acc/codex"
)

type codexAuthAdapter struct{ auth *authManager }

func (a codexAuthAdapter) StoreReady() bool { return a.auth != nil && a.auth.store != nil }
func (a codexAuthAdapter) StoreName() string {
	if a.auth == nil {
		return ""
	}
	return a.auth.storeName
}

func init() {
	codex.LoadRuntimeConfig = func() (*codex.Config, codex.AuthManager, error) {
		loadDotenv(defaultEnvPath())
		cfg, err := loadConfig(defaultConfigPath())
		if err != nil {
			return nil, nil, err
		}
		if err := validateConfig(cfg); err != nil {
			return nil, nil, err
		}
		auth, authErr := newDefaultAuthManager()
		if authErr != nil {
			auth.storeName = "unavailable: " + authErr.Error()
		}
		return cfg, codexAuthAdapter{auth}, nil
	}
	codex.AccDir = accDir
	codex.DefaultEnvPath = defaultEnvPath
	codex.DefaultConfigPath = defaultConfigPath
	codex.LoadDotenv = loadDotenv

	claude.LoadDotenv = loadDotenv
	claude.DefaultEnvPath = defaultEnvPath
	claude.LoadConfig = loadConfig
	claude.DefaultConfigPath = defaultConfigPath
	claude.RandID = randID
	claude.Truncate = truncate
}
