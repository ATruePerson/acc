package codex

// Runtime hooks wired from the main package so codex CLI/lifecycle code can
// load ACC config and auth without importing package main.
var (
	LoadRuntimeConfig func() (*Config, AuthManager, error)
	AccDir            func() string
	DefaultEnvPath    func() string
	DefaultConfigPath func() string
	LoadDotenv        func(string)
)

// AuthManager exposes only what Codex lifecycle/doctor code needs from ACC auth.
type AuthManager interface {
	StoreReady() bool
	StoreName() string
}
