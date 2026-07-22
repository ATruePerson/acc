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

// legacyCodexModelID keeps tasks created by older ACC catalogs working. These
// IDs remain accepted for history compatibility but are never advertised in
// the provider-prefixed Codex catalog.
func legacyCodexModelID(modelID string) string {
	switch normalizeModelID(modelID) {
	case "opus", "openai/codex-5.6-sol":
		return codexOpusID
	case "sonnet", "openai/codex-5.6-terra":
		return codexSonnetID
	case "haiku", "openai/codex-5.6-luna":
		return codexHaikuID
	default:
		return modelID
	}
}

// normalizeLegacyResponsesRequest upgrades old IDs and the retired High effort
// name. Current public models expose only Max, including resumed older tasks.
func normalizeLegacyResponsesRequest(req *ResponsesRequest) {
	if req == nil {
		return
	}
	req.Model = legacyCodexModelID(req.Model)
	if req.Reasoning != nil && req.Reasoning.Effort == "high" &&
		(req.Model == codexOpusID || req.Model == codexSonnetID || req.Model == codexHaikuID) {
		req.Reasoning.Effort = "max"
	}
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
	resolvedID := legacyCodexModelID(modelID)
	if capability, ok := cfg.Models[resolvedID]; ok {
		if !capability.Enabled {
			return nil, fmt.Errorf("selected model %q is disabled", modelID)
		}
		route, err := resolveCapabilityRoute(cfg, resolvedID, capability)
		if err != nil {
			return nil, err
		}
		chain := []resolvedModel{{ID: resolvedID, Capability: capability, Route: route}}
		seen := map[string]bool{resolvedID: true}
		for _, fallbackID := range configuredFallbackModels(capability) {
			fallback, ok := cfg.Models[fallbackID]
			if !ok || !fallback.Enabled {
				return nil, fmt.Errorf("model %q configures unavailable fallback model %q", resolvedID, fallbackID)
			}
			fallbackRoute, err := resolveCapabilityRoute(cfg, fallbackID, fallback)
			if err != nil {
				return nil, err
			}
			chain = append(chain, resolvedModel{ID: fallbackID, Capability: fallback, Route: fallbackRoute, Fallback: true})
			seen[fallbackID] = true
		}
		if capability.ImageModel != "" && !seen[capability.ImageModel] {
			imageCapability, ok := cfg.Models[capability.ImageModel]
			if !ok || !imageCapability.Enabled {
				return nil, fmt.Errorf("model %q configures unavailable image model %q", resolvedID, capability.ImageModel)
			}
			imageRoute, err := resolveCapabilityRoute(cfg, capability.ImageModel, imageCapability)
			if err != nil {
				return nil, err
			}
			chain = append(chain, resolvedModel{ID: capability.ImageModel, Capability: imageCapability, Route: imageRoute, Fallback: true, ImageOnly: true})
			seen[capability.ImageModel] = true
		}
		for _, imageFallbackID := range capability.ImageFallbackModels {
			if imageFallbackID == "" || seen[imageFallbackID] {
				continue
			}
			imageFallback, ok := cfg.Models[imageFallbackID]
			if !ok || !imageFallback.Enabled {
				return nil, fmt.Errorf("model %q configures unavailable image fallback model %q", resolvedID, imageFallbackID)
			}
			imageFallbackRoute, err := resolveCapabilityRoute(cfg, imageFallbackID, imageFallback)
			if err != nil {
				return nil, err
			}
			chain = append(chain, resolvedModel{ID: imageFallbackID, Capability: imageFallback, Route: imageFallbackRoute, Fallback: true, ImageOnly: true})
			seen[imageFallbackID] = true
		}
		return chain, nil
	}

	if provider, upstreamModel, ok := splitRealCodexModelID(resolvedID); ok {
		if _, exists := cfg.Providers[provider]; !exists {
			return nil, fmt.Errorf("selected Codex provider %q is unavailable", provider)
		}
		capability := ModelCapability{
			DisplayName: provider + "/" + upstreamModel, Provider: provider, Model: upstreamModel,
			Enabled: true, StreamingSupport: true, ToolCallSupport: true,
			Reasoning: map[string]ReasoningTarget{
				"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
				"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"}, "max": {Effort: "max"},
			},
		}
		for id, configured := range cfg.Models {
			if !configured.Enabled {
				continue
			}
			route, err := resolveCapabilityRoute(cfg, id, configured)
			if err == nil && route.Provider == provider && route.Model == upstreamModel {
				capability = configured
				capability.Provider, capability.Model, capability.Route = provider, upstreamModel, ""
				break
			}
		}
		route := Route{Provider: provider, Model: upstreamModel, Reasoning: capability.Reasoning}
		if capability.MaxOutput > 0 {
			route.MaxTokens = capability.MaxOutput
		}
		return []resolvedModel{{ID: resolvedID, Capability: capability, Route: route}}, nil
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

func configuredFallbackModels(capability ModelCapability) []string {
	ids := make([]string, 0, len(capability.FallbackModels)+1)
	seen := map[string]bool{}
	if capability.FallbackModel != "" {
		ids = append(ids, capability.FallbackModel)
		seen[capability.FallbackModel] = true
	}
	for _, id := range capability.FallbackModels {
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
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

// selectResponseModelChain removes only explicitly incompatible routes. For
// Opus image input this skips text-only GLM and begins on MiniMax. It never
// reintroduces a text-only route later in the fallback chain.
func selectResponseModelChain(req *ResponsesRequest, routes []resolvedModel) ([]resolvedModel, error) {
	return selectResponseModelChainForInput(req, routes, 0)
}

func selectResponseModelChainForInput(req *ResponsesRequest, routes []resolvedModel, estimatedInputTokens int) ([]resolvedModel, error) {
	requirements, err := responseInputRequirements(req)
	if err != nil {
		return nil, err
	}
	needsTools := len(req.Tools) > 0
	needsStreaming := req.Stream
	terraTools := legacyCodexModelID(req.Model) == codexSonnetID && needsTools
	publicContext := 0
	if len(routes) > 0 {
		publicContext = routes[0].Capability.MaxContext
	}
	eligible := make([]resolvedModel, 0, len(routes))
	skippedForContext := false
	for _, target := range routes {
		capability := target.Capability
		if target.ImageOnly && !requirements.Image {
			continue
		}
		if capability.DisplayName == "" { // Preserve legacy behavior where no capability metadata exists.
			eligible = append(eligible, target)
			continue
		}
		if requirements.Image && !capability.ImageInputSupport {
			continue
		}
		if requirements.File && !capability.FileInputSupport {
			continue
		}
		if needsTools && !capability.ToolCallSupport {
			continue
		}
		if needsStreaming && !capability.StreamingSupport {
			continue
		}
		maxContext := target.Route.MaxContext
		if maxContext == 0 {
			maxContext = capability.MaxContext
		}
		if terraTools && publicContext > 0 && maxContext > 0 && maxContext < publicContext {
			skippedForContext = true
			continue
		}
		if legacyCodexModelID(req.Model) == codexSonnetID && estimatedInputTokens > 0 {
			maxOutput := responseOutputBudget(req, target)
			if maxContext > 0 && estimatedInputTokens+maxOutput > maxContext {
				skippedForContext = true
				continue
			}
		}
		eligible = append(eligible, target)
	}
	if len(eligible) == 0 {
		switch {
		case skippedForContext:
			return nil, fmt.Errorf("model %q request is too large for every configured route; use 5.6 Sol or 5.6 Luna, or start a fresh thread", req.Model)
		case requirements.Image && needsTools:
			return nil, fmt.Errorf("model %q has no configured route that supports both image input and tool calls", req.Model)
		case requirements.File && needsTools:
			return nil, fmt.Errorf("model %q has no configured route that supports both file input and tool calls", req.Model)
		case requirements.Image && requirements.File:
			return nil, fmt.Errorf("model %q has no configured route that supports both image and file input", req.Model)
		case requirements.Image:
			return nil, fmt.Errorf("model %q has no configured image-capable route", req.Model)
		case requirements.File:
			return nil, fmt.Errorf("model %q has no configured file-capable route", req.Model)
		case needsTools:
			return nil, fmt.Errorf("model %q has no configured route that supports tool calls", req.Model)
		case needsStreaming:
			return nil, fmt.Errorf("model %q has no configured route that supports streaming", req.Model)
		default:
			return nil, fmt.Errorf("model %q has no configured compatible route", req.Model)
		}
	}
	if eligible[0].ID != routes[0].ID {
		eligible[0].Fallback = false
		eligible[0].CapabilityReroute = true
	}
	for i := 1; i < len(eligible); i++ {
		eligible[i].Fallback = true
	}
	return eligible, nil
}

// responseOutputBudget returns the output space this request can actually use
// on one route. A smaller client limit must not be replaced by the route's
// configured ceiling when deciding whether the request fits.
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
