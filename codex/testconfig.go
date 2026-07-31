package codex

const (
	codexOpusID    = "opus"
	codexSonnetID  = "sonnet"
	codexHaikuID   = "haiku"
)

// TestConfig returns a sample configuration for use in tests.
func TestConfig() *Config { return codexTestConfig() }

// ModelCatalogJSON returns catalog JSON for cfg without auth filtering.
func ModelCatalogJSON(cfg *Config) []byte { return codexModelCatalogJSON(cfg) }

func codexTestConfig() *Config {
	return &Config{
		Providers: map[string]Provider{
			"nvidia":     {BaseURL: "https://nvidia.test", APIKey: "nvidia-key"},
			"opencode":   {BaseURL: "https://opencode.test", APIKey: "opencode-key"},
			"openrouter": {BaseURL: "https://openrouter.test", APIKey: "openrouter-key"},
		},
		Routes: map[string]Route{
			"sol":   {Provider: "nvidia", Model: "z-ai/glm-5.2", Toolcalling: boolPtr(true)},
			"terra": {Provider: "opencode", Model: "big-pickle", MaxContext: 131072, MaxTokens: 48000, Toolcalling: boolPtr(true)},
			"luna":  {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", Toolcalling: boolPtr(true)},
		},
		AliasRoutes: map[string]Route{
			"opus":   {Provider: "nvidia", Model: "z-ai/glm-5.2", Toolcalling: boolPtr(true)},
			"sonnet": {Provider: "opencode", Model: "big-pickle", Toolcalling: boolPtr(true)},
			"haiku":  {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", Toolcalling: boolPtr(true)},
		},
		Models: map[string]ModelCapability{
			codexOpusID: {
				DisplayName: "5.6 Sol", CatalogPriority: 1, Route: "sol", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"},
				},
				ToolCallSupport: true, StreamingSupport: true, ImageInputSupport: true,
				MaxContext: 131072, MaxOutput: 131072,
			},
			codexSonnetID: {
				DisplayName: "5.6 Terra", CatalogPriority: 2, Route: "terra", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"}, "max": {Effort: "max"},
				},
				ToolCallSupport: true, StreamingSupport: true, FileInputSupport: true,
				MaxContext: 262144, MaxOutput: 65536,
			},
			codexHaikuID: {
				DisplayName: "5.6 Luna", CatalogPriority: 3, Route: "luna", Enabled: true,
				Reasoning:        map[string]ReasoningTarget{"minimal": {}, "low": {Effort: "low"}},
				StreamingSupport: true, ImageInputSupport: true,
				MaxContext: 131072, MaxOutput: 26000,
			},
			"disabled-model": {
				DisplayName: "Disabled", Provider: "nvidia", Model: "disabled", Enabled: false,
			},
		},
	}
}

// boolPtr is a helper to create a pointer to a bool value.
func boolPtr(v bool) *bool { return &v }