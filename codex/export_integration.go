package codex

// RoutingState summarizes how Codex is currently configured.
type RoutingState = codexRoutingState

// ConfigureResult reports what changed during a Codex configure transaction.
type ConfigureResult = codexConfigureResult

// ConfigTransaction stages Codex file mutations with rollback support.
type ConfigTransaction = codexConfigTransaction

// HomeDir returns the resolved Codex home directory.
func HomeDir() (string, error) { return codexHomeDir() }

// InspectRouting parses Codex config text into a routing summary.
func InspectRouting(config string) RoutingState { return inspectCodexRouting(config) }

// BaselineStatus describes whether a subscription baseline is present.
func BaselineStatus(path, currentConfig string) string { return codexBaselineStatus(path, currentConfig) }

// BeginConfigureApp starts a staged Codex configure transaction.
func BeginConfigureApp(configPath, catalogPath, baselinePath, restartPath, baseURL, model string, cfg *Config, auth AuthManager) (*ConfigTransaction, error) {
	return beginConfigureCodexApp(configPath, catalogPath, baselinePath, restartPath, baseURL, model, cfg, auth)
}

// ValidateLoopbackBaseURL ensures ACC's Codex gateway URL is a local loopback address.
func ValidateLoopbackBaseURL(raw string) error { return validateCodexLoopbackBaseURL(raw) }

// ValidateConfigText checks Codex TOML syntax before writing files.
func ValidateConfigText(text string) error { return validateCodexConfigText(text) }

// FrontGatewayURL builds the ACC gateway URL Codex should point at.
func FrontGatewayURL(cfg *Config) string { return codexFrontGatewayURL(cfg) }

// ModelCatalogEntries returns catalog picker entries for cfg.
func ModelCatalogEntries(cfg *Config) []map[string]any {
	return codexModelCatalogEntries(cfg)
}
func ModelCatalogEntriesWithAuth(cfg *Config, auth AuthManager) []map[string]any {
	return codexModelCatalogEntriesWithAuth(cfg, auth)
}

// ModelCatalogJSONWithAuth returns the acc-models.json payload for configured models.
func ModelCatalogJSONWithAuth(cfg *Config, auth AuthManager) []byte {
	return codexModelCatalogJSONWithAuth(cfg, auth)
}

// CatalogHasModel reports whether a catalog JSON body includes the given model slug.
func CatalogHasModel(body []byte, model string) bool { return catalogHasCodexModel(body, model) }

// ClearRestartRequired removes the ChatGPT restart marker file.
func ClearRestartRequired(path string) error { return clearCodexRestartRequired(path) }

// ReadBaseline loads a Codex subscription baseline from disk.
func ReadBaseline(path string) (*codexSubscriptionBaseline, error) {
	return readCodexBaseline(path)
}

// ValidateBaseline checks baseline version and required snapshots.
func ValidateBaseline(baseline *codexSubscriptionBaseline) error {
	return validateCodexBaseline(baseline)
}

// ResolveCodexPath resolves a catalog path relative to a Codex config file.
func ResolveCodexPath(path, configPath string) string { return resolveCodexPath(path, configPath) }
