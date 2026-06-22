package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderEnvSortedAndPrivate(t *testing.T) {
	out := renderEnv(map[string]string{
		"OPENCODE_API_KEY":   "ock",
		"NVIDIA_NIM_API_KEY": "nvk",
	})
	if !strings.Contains(out, "NVIDIA_NIM_API_KEY=nvk") {
		t.Fatalf("missing nvidia key:\n%s", out)
	}
	// sorted: NVIDIA before OPENCODE
	if strings.Index(out, "NVIDIA_NIM_API_KEY") > strings.Index(out, "OPENCODE_API_KEY") {
		t.Fatalf("keys not sorted:\n%s", out)
	}
}

func TestDefaultConfigIsValidAndLoads(t *testing.T) {
	if !json.Valid([]byte(defaultConfigJSON)) {
		t.Fatal("defaultConfigJSON is not valid JSON")
	}
	var c Config
	if err := json.Unmarshal([]byte(defaultConfigJSON), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Port == 0 || len(c.Providers) == 0 || len(c.Routes) == 0 {
		t.Fatalf("default config missing essentials: %+v", c)
	}
	// every route must point at a defined provider
	if err := validateConfig(&c); err != nil {
		t.Fatalf("default config fails validation: %v", err)
	}
}

func TestKnownProvidersHaveEnvVars(t *testing.T) {
	for _, p := range knownProviders() {
		if p.Key == "" || p.EnvVar == "" || p.BaseURL == "" {
			t.Errorf("incomplete provider: %+v", p)
		}
	}
}
