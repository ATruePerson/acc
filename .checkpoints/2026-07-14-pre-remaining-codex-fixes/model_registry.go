package main

import (
	"fmt"
	"sort"
	"strings"
)

type resolvedModel struct {
	ID         string
	Capability ModelCapability
	Route      Route
	Fallback   bool
}

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

func resolveCapabilityRoute(cfg *Config, id string, capability ModelCapability) (Route, error) {
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

func (s *server) responseModelChain(modelID string) ([]resolvedModel, error) {
	cfg := s.cfg.Load()
	if capability, ok := cfg.Models[modelID]; ok {
		if !capability.Enabled {
			return nil, fmt.Errorf("selected model %q is disabled", modelID)
		}
		route, err := resolveCapabilityRoute(cfg, modelID, capability)
		if err != nil {
			return nil, err
		}
		chain := []resolvedModel{{ID: modelID, Capability: capability, Route: route}}
		if capability.FallbackModel != "" {
			fallback, ok := cfg.Models[capability.FallbackModel]
			if !ok || !fallback.Enabled {
				return nil, fmt.Errorf("model %q configures unavailable fallback model %q", modelID, capability.FallbackModel)
			}
			fallbackRoute, err := resolveCapabilityRoute(cfg, capability.FallbackModel, fallback)
			if err != nil {
				return nil, err
			}
			chain = append(chain, resolvedModel{ID: capability.FallbackModel, Capability: fallback, Route: fallbackRoute, Fallback: true})
		}
		return chain, nil
	}

	// Legacy non-Codex clients can still use aliases and route fallbacks. They do
	// not become selectable Codex catalog entries until explicitly registered.
	route, err := s.routeFor(modelID)
	if err != nil {
		return nil, err
	}
	chain := []resolvedModel{{ID: modelID, Route: route}}
	for _, fallback := range route.Fallbacks {
		chain = append(chain, resolvedModel{ID: fallback.Provider + "/" + fallback.Model, Route: fallback, Fallback: true})
	}
	return chain, nil
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

func validateRequestedEffort(target resolvedModel, effort string) error {
	if effort == "" {
		return nil
	}
	if target.Capability.DisplayName == "" {
		_, err := exactProviderReasoningEffort(target.Route.Provider, effort)
		return err
	}
	if _, ok := target.Capability.Reasoning[effort]; !ok {
		return fmt.Errorf("model %q does not support reasoning effort %q", target.ID, effort)
	}
	return nil
}

func applyReasoningTarget(req *OpenAIRequest, target resolvedModel, requested string) (map[string]any, error) {
	if requested == "" {
		if target.Route.ReasoningEffort != "" {
			req.ReasoningEffort = target.Route.ReasoningEffort
		}
		return nil, nil
	}
	if target.Capability.DisplayName == "" {
		effort, err := exactProviderReasoningEffort(target.Route.Provider, requested)
		if err != nil {
			return nil, err
		}
		req.ReasoningEffort = effort
		return nil, nil
	}
	if err := validateRequestedEffort(target, requested); err != nil {
		return nil, err
	}
	mapping := target.Capability.Reasoning[requested]
	req.ReasoningEffort = mapping.Effort
	return mapping.ExtraBody, nil
}

func validateResponseCapabilities(req *ResponsesRequest, target resolvedModel) error {
	capability := target.Capability
	if len(capability.DisplayName) == 0 { // Legacy request without registry metadata.
		return nil
	}
	if req.Stream && !capability.StreamingSupport {
		return fmt.Errorf("model %q does not support streaming", target.ID)
	}
	if len(req.Tools) > 0 && !capability.ToolCallSupport {
		return fmt.Errorf("model %q does not support tool calls", target.ID)
	}
	input := string(req.Input)
	if strings.Contains(input, `"type":"input_image"`) && !capability.ImageInputSupport {
		return fmt.Errorf("model %q does not support image input", target.ID)
	}
	if strings.Contains(input, `"type":"input_file"`) && !capability.FileInputSupport {
		return fmt.Errorf("model %q does not support file input", target.ID)
	}
	return nil
}
