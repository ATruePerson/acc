package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type resolvedModel struct {
	ID                string
	Capability        ModelCapability
	Route             Route
	Fallback          bool
	CapabilityReroute bool
	ImageOnly         bool
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
	if capability.Route != "" {
		return Route{}, fmt.Errorf("Codex model %q uses legacy route indirection %q; configure provider and model directly", id, capability.Route)
	}
	if capability.FallbackModel != "" || len(capability.FallbackModels) > 0 || capability.ImageModel != "" || len(capability.ImageFallbackModels) > 0 {
		return Route{}, fmt.Errorf("Codex model %q configures automatic fallback or capability rerouting; register each provider/model as its own catalog entry", id)
	}
	route := Route{Provider: capability.Provider, Model: capability.Model}
	if route.Provider == "" || route.Model == "" {
		return Route{}, fmt.Errorf("Codex model %q has no direct provider/model route", id)
	}
	if _, ok := cfg.Providers[route.Provider]; !ok {
		return Route{}, fmt.Errorf("Codex model %q uses unavailable provider %q", id, route.Provider)
	}
	if len(capability.Reasoning) > 0 {
		route.Reasoning = capability.Reasoning
	}
	if capability.MaxOutput > 0 {
		route.MaxTokens = capability.MaxOutput
	}
	return route, nil
}

func (s *server) responseModelChain(modelID string) ([]resolvedModel, error) {
	cfg := s.cfg.Load()
	provider, upstreamModel, ok := splitRealCodexModelID(modelID)
	if !ok {
		return nil, fmt.Errorf("Codex model %q must be an exact provider/model ID from the configured catalog", modelID)
	}
	if _, exists := cfg.Providers[provider]; !exists {
		return nil, fmt.Errorf("selected Codex provider %q is unavailable", provider)
	}
	for _, model := range codexNamedModelsWithAuth(cfg, s.auth) {
		if model.ID != modelID {
			continue
		}
		if model.Route.Provider != provider || model.Route.Model != upstreamModel {
			return nil, fmt.Errorf("Codex catalog entry %q does not match its provider/model route", modelID)
		}
		return []resolvedModel{{ID: model.ID, Capability: model.Capability, Route: model.Route}}, nil
	}
	return nil, fmt.Errorf("selected Codex model %q is not in the configured catalog", modelID)
}

func splitRealCodexModelID(modelID string) (provider, model string, ok bool) {
	provider, model, ok = strings.Cut(modelID, "/")
	if !ok || provider == "" || model == "" {
		return "", "", false
	}
	if _, isAlias := aliasFamily(normalizeModelID(provider)); isAlias {
		return "", "", false
	}
	return provider, model, true
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
	if len(capability.DisplayName) == 0 {
		return nil
	}
	if req.Stream && !capability.StreamingSupport {
		return fmt.Errorf("model %q does not support streaming", target.ID)
	}
	if len(req.Tools) > 0 && !capability.ToolCallSupport {
		return fmt.Errorf("model %q does not support tool calls", target.ID)
	}
	requirements, err := responseInputRequirements(req)
	if err != nil {
		return err
	}
	if requirements.Image && !capability.ImageInputSupport {
		return fmt.Errorf("model %q does not support image input", target.ID)
	}
	if requirements.File && !capability.FileInputSupport {
		return fmt.Errorf("model %q does not support file input", target.ID)
	}
	return nil
}

type responseInputRequirement struct {
	Image bool
	File  bool
}

func responseInputRequirements(req *ResponsesRequest) (responseInputRequirement, error) {
	var requirements responseInputRequirement
	if len(req.Input) == 0 {
		return requirements, nil
	}
	var inputText string
	if err := json.Unmarshal(req.Input, &inputText); err == nil {
		return requirements, nil
	}
	var items []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return requirements, fmt.Errorf("bad input: %w", err)
	}
	for _, item := range items {
		if item.Type != "message" || len(item.Content) == 0 {
			continue
		}
		var text string
		if json.Unmarshal(item.Content, &text) == nil {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item.Content, &parts); err != nil {
			return requirements, fmt.Errorf("bad message content: %w", err)
		}
		for _, part := range parts {
			switch part.Type {
			case "input_image", "image_url":
				requirements.Image = true
			case "input_file", "file":
				requirements.File = true
			}
		}
	}
	return requirements, nil
}

// selectResponseModelChain validates the one model the user selected. It never
// substitutes another provider or upstream model for capability or context reasons.
func selectResponseModelChain(req *ResponsesRequest, routes []resolvedModel) ([]resolvedModel, error) {
	return selectResponseModelChainForInput(req, routes, 0)
}

func selectResponseModelChainForInput(req *ResponsesRequest, routes []resolvedModel, estimatedInputTokens int) ([]resolvedModel, error) {
	if len(routes) != 1 {
		return nil, fmt.Errorf("Codex requires exactly one selected provider/model route, got %d", len(routes))
	}
	target := routes[0]
	if target.Fallback || target.CapabilityReroute || target.ImageOnly {
		return nil, fmt.Errorf("Codex route %q is not a direct selected model", target.ID)
	}
	if err := validateResponseCapabilities(req, target); err != nil {
		return nil, err
	}
	maxContext := target.Route.MaxContext
	if maxContext == 0 {
		maxContext = target.Capability.MaxContext
	}
	if estimatedInputTokens > 0 && maxContext > 0 && estimatedInputTokens+responseOutputBudget(req, target) > maxContext {
		return nil, fmt.Errorf("request is too large for selected Codex model %q", target.ID)
	}
	return routes, nil
}

func responseOutputBudget(req *ResponsesRequest, target resolvedModel) int {
	requested := req.MaxOutputTokens
	if requested == 0 {
		requested = req.MaxTokens
	}
	routeLimit := target.Route.MaxTokens
	if routeLimit == 0 {
		routeLimit = target.Capability.MaxOutput
	}
	if requested == 0 {
		return routeLimit
	}
	if routeLimit > 0 && requested > routeLimit {
		return routeLimit
	}
	return requested
}
