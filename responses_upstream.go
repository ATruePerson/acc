package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (s *server) executeUpstream(
	ctx context.Context,
	or *OpenAIRequest,
	routes []resolvedModel,
	cfg *Config,
	logit func(routeModel string, status, in, out int, effort string),
	w http.ResponseWriter,
) (*http.Response, resolvedModel, error) {
	if len(routes) != 1 {
		err := fmt.Errorf("Codex requires exactly one selected provider/model route, got %d", len(routes))
		httpErr(w, http.StatusBadRequest, err.Error())
		return nil, resolvedModel{}, err
	}
	target := routes[0]
	if target.Fallback || target.CapabilityReroute || target.ImageOnly {
		err := fmt.Errorf("Codex route %q is not a direct selected model", target.ID)
		httpErr(w, http.StatusBadRequest, err.Error())
		return nil, resolvedModel{}, err
	}
	currentRoute := target.Route
	runtime, err := resolveProviderRuntime(ctx, cfg, s.auth, currentRoute.Provider, false)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		logit(currentRoute.Model, http.StatusInternalServerError, 0, 0, "")
		return nil, resolvedModel{}, err
	}
	requestCopy := *or
	requestForRoute := &requestCopy
	requestForRoute.Model = currentRoute.Model
	if currentRoute.Temperature != nil {
		requestForRoute.Temperature = currentRoute.Temperature
	}
	if currentRoute.TopP != nil {
		requestForRoute.TopP = currentRoute.TopP
	}
	requestForRoute.MaxTokens = boundedOutputTokens(or.MaxTokens, currentRoute.MaxTokens)
	requestedReasoningEffort := or.ReasoningEffort
	requestForRoute.ReasoningEffort = ""
	effortExtra, err := applyReasoningTarget(requestForRoute, target, requestedReasoningEffort)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(currentRoute.Model, http.StatusBadRequest, 0, 0, "")
		return nil, resolvedModel{}, err
	}
	body, _ := json.Marshal(requestForRoute)
	body = mergeRouteExtraBody(body, currentRoute.ExtraBody)
	body = mergeRouteExtraBody(body, effortExtra)
	if err := s.limiter.Wait(ctx, currentRoute.Provider); err != nil {
		httpErr(w, http.StatusGatewayTimeout, fmt.Sprintf("rate limiter interrupted for %s/%s: %v", currentRoute.Provider, currentRoute.Model, err))
		logit(currentRoute.Model, http.StatusGatewayTimeout, 0, 0, requestForRoute.ReasoningEffort)
		return nil, resolvedModel{}, err
	}
	resp, err := doProviderRequestWithBody(ctx, s.http, s.auth, runtime, requestForRoute, body)
	if err != nil {
		message := fmt.Sprintf("upstream %s/%s: %v", currentRoute.Provider, currentRoute.Model, err)
		httpErr(w, http.StatusBadGateway, message)
		logit(currentRoute.Model, http.StatusBadGateway, 0, 0, requestForRoute.ReasoningEffort)
		return nil, resolvedModel{}, err
	}
	if requestForRoute.Stream && resp.StatusCode < http.StatusBadRequest {
		reader, timedOut := awaitFirstByte(resp.Body, firstTokenTimeout)
		if timedOut {
			resp.Body.Close()
			err := fmt.Errorf("upstream %s/%s produced no response within %s", currentRoute.Provider, currentRoute.Model, firstTokenTimeout)
			httpErr(w, http.StatusGatewayTimeout, err.Error())
			logit(currentRoute.Model, http.StatusGatewayTimeout, 0, 0, requestForRoute.ReasoningEffort)
			return nil, resolvedModel{}, err
		}
		resp.Body = io.NopCloser(reader)
	}
	return resp, target, nil
}
