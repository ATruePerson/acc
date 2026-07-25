package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.reloadIfChanged()
	cfg := s.cfg.Load()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "read body: "+err.Error())
		return
	}

	var req ResponsesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		httpErr(w, 400, "parse request: "+err.Error())
		return
	}

	logit := func(routeModel string, status, in, out int, effort string) {
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     req.Model,
			Route:     routeModel,
			Status:    status,
			TokensIn:  in,
			TokensOut: out,
			Budget:    0,
			Effort:    effort,
			CostUSD:   costFor(routeModel, in, out, cfg),
		})
	}
	if err := s.applyPreviousResponse(&req); err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		logit("error", http.StatusNotFound, 0, 0, "")
		return
	}
	requestForSizing, _ := json.Marshal(req)

	routes, err := s.responseModelChain(req.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}
	routes, err = selectResponseModelChainForInput(&req, routes, estimateResponsesInputTokens(requestForSizing))
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}

	primary := routes[0]
	if err := validateResponseCapabilities(&req, primary); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(primary.Route.Model, http.StatusBadRequest, 0, 0, "")
		return
	}
	requestedEffort := ""
	if req.Reasoning != nil {
		requestedEffort = req.Reasoning.Effort
	}
	if err := validateRequestedEffort(primary, requestedEffort); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(primary.Route.Model, http.StatusBadRequest, 0, 0, "")
		return
	}

	or, translation, err := translateFromResponsesWithTools(&req, primary.Route, cfg)
	if err != nil {
		httpErr(w, 400, "translate: "+err.Error())
		logit(primary.Route.Model, 400, 0, 0, "")
		return
	}
	or.Temperature = req.Temperature
	or.TopP = req.TopP
	or.MaxTokens = req.MaxOutputTokens
	if or.MaxTokens == 0 {
		or.MaxTokens = req.MaxTokens
	}

	resp, activeRoute, err := s.executeUpstream(r.Context(), or, routes, cfg, logit, w)
	if err != nil {
		return
	}
	setBackendHeaders(w, req.Model, activeRoute, requestedEffort)
	log.Printf("responses: model=%s -> %s/%s effort=%s", req.Model, activeRoute.Route.Provider, activeRoute.Route.Model, backendEffort(activeRoute, requestedEffort))
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		detail := strings.TrimSpace(string(b))
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, req.Model, activeRoute.Route.Provider, activeRoute.Route.Model, truncate(detail, 500))
		httpErr(w, resp.StatusCode, fmt.Sprintf("upstream %s/%s: %s", activeRoute.Route.Provider, activeRoute.Route.Model, detail))
		logit(activeRoute.Route.Model, resp.StatusCode, 0, 0, or.ReasoningEffort)
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		inTokens, outTokens := streamTranslateResponsesWithCompletion(w, resp.Body, req.Model, func(response *ResponsesResponse) {
			if responsesShouldStore(&req) {
				s.rememberResponse(response)
			}
		}, translation)
		logit(activeRoute.Route.Model, resp.StatusCode, inTokens, outTokens, or.ReasoningEffort)
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		logit(activeRoute.Route.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}

	out, err := translateToResponsesWithTools(&oresp, req.Model, translation)
	if err != nil {
		httpErr(w, 502, "translate upstream response: "+err.Error())
		logit(activeRoute.Route.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}
	if responsesShouldStore(&req) {
		s.rememberResponse(out)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn, tokensOut := 0, 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	logit(activeRoute.Route.Model, resp.StatusCode, tokensIn, tokensOut, or.ReasoningEffort)
}

func responsesShouldStore(req *ResponsesRequest) bool {
	return req == nil || req.Store == nil || *req.Store
}

func estimateResponsesInputTokens(raw []byte) int {
	return (len(raw) + 2) / 3
}

func boundedOutputTokens(requested, routeLimit int) int {
	switch {
	case requested <= 0:
		return routeLimit
	case routeLimit <= 0 || requested <= routeLimit:
		return requested
	default:
		return routeLimit
	}
}

func setBackendHeaders(w http.ResponseWriter, requestedModel string, active resolvedModel, requestedEffort string) {
	w.Header().Set("X-ACC-Requested-Model", requestedModel)
	w.Header().Set("X-ACC-Backend-Provider", active.Route.Provider)
	w.Header().Set("X-ACC-Backend-Model", active.Route.Model)
	if requestedEffort != "" {
		w.Header().Set("X-ACC-Requested-Effort", requestedEffort)
	}
	w.Header().Set("X-ACC-Backend-Effort", backendEffort(active, requestedEffort))
}

func backendEffort(active resolvedModel, requested string) string {
	actual := active.Route.ReasoningEffort
	if requested != "" {
		actual = requested
		if active.Capability.DisplayName != "" {
			actual = active.Capability.Reasoning[requested].Effort
		}
	}
	if actual == "" {
		return "none"
	}
	return actual
}
