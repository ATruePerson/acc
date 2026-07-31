package codex

import (
	"fmt"
	"sort"
)

func enabledModelIDs(cfg *Config) []string {
	ids := make([]string, 0, len(cfg.Models))
	for id, capability := range cfg.Models {
		if capability.Enabled {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func ResolveCapabilityRoute(cfg *Config, id string, capability ModelCapability) (Route, error) {
	var route Route
	if capability.Route != "" {
		var ok bool
		route, ok = cfg.Routes[capability.Route]
		if !ok {
			return Route{}, fmt.Errorf("model %q references unavailable route %q", id, capability.Route)
		}
	} else {
		route = Route{Provider: capability.Provider, Model: capability.Model}
	}
	if route.Provider == "" || route.Model == "" {
		return Route{}, fmt.Errorf("model %q has no provider/model route", id)
	}
	if _, ok := cfg.Providers[route.Provider]; !ok {
		return Route{}, fmt.Errorf("model %q uses unavailable provider %q", id, route.Provider)
	}
	if len(capability.Reasoning) > 0 {
		route.Reasoning = capability.Reasoning
	}
	if capability.MaxOutput > 0 && route.MaxTokens == 0 {
		route.MaxTokens = capability.MaxOutput
	}
	return route, nil
}

func supportedEfforts(capability ModelCapability) []string {
	order := []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	levels := make([]string, 0, len(capability.Reasoning))
	for _, effort := range order {
		if _, ok := capability.Reasoning[effort]; ok {
			levels = append(levels, effort)
		}
	}
	return levels
}
