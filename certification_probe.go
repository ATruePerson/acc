package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type certifyHTTPResult struct {
	Status   int
	Header   http.Header
	Body     []byte
	Duration time.Duration
	Err      error
}

func certifyPost(client *http.Client, endpoint string, payload map[string]any) certifyHTTPResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return certifyHTTPResult{Err: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return certifyHTTPResult{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := client.Do(req)
	result := certifyHTTPResult{Duration: time.Since(started), Err: err}
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	result.Status = resp.StatusCode
	result.Header = resp.Header.Clone()
	result.Body, result.Err = io.ReadAll(resp.Body)
	return result
}

func certifyCompletedResponse(result certifyHTTPResult, label string) CertificationCheck {
	check := baseCertificationCheck(result)
	if check.Status != "" {
		return check
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result.Body, &response); err != nil {
		return CertificationCheck{Status: certFail, Detail: label + ": invalid JSON response", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	if response.Status != "completed" {
		return CertificationCheck{Status: certFail, Detail: label + ": response status " + response.Status, LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	return CertificationCheck{Status: certPass, Detail: label, LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
}

func certifyStreamingResponse(result certifyHTTPResult) CertificationCheck {
	check := baseCertificationCheck(result)
	if check.Status != "" {
		return check
	}
	body := string(result.Body)
	if !strings.Contains(body, "event: response.completed") || strings.Contains(body, "event: response.incomplete") {
		return CertificationCheck{Status: certFail, Detail: "stream did not complete cleanly", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	return CertificationCheck{Status: certPass, Detail: "SSE completed", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
}

func certifyOutputItem(result certifyHTTPResult, itemType, name, namespace string) CertificationCheck {
	check := baseCertificationCheck(result)
	if check.Status != "" {
		return check
	}
	var response struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(result.Body, &response); err != nil {
		return CertificationCheck{Status: certFail, Detail: "invalid JSON tool response", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	for _, item := range response.Output {
		if item["type"] != itemType || item["name"] != name {
			continue
		}
		if namespace != "" && item["namespace"] != namespace {
			continue
		}
		return CertificationCheck{Status: certPass, Detail: itemType + " emitted", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	return CertificationCheck{Status: certFail, Detail: fmt.Sprintf("expected %s %q was not emitted", itemType, name), LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
}

func baseCertificationCheck(result certifyHTTPResult) CertificationCheck {
	if result.Err != nil {
		return CertificationCheck{Status: certFail, Detail: result.Err.Error(), LatencyMS: result.Duration.Milliseconds()}
	}
	if result.Status == http.StatusTooManyRequests {
		return CertificationCheck{Status: certBlocked, Detail: "selected provider rate limit or quota exhausted", LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	if result.Status < 200 || result.Status >= 300 {
		detail := strings.TrimSpace(string(result.Body))
		if len(detail) > 240 {
			detail = detail[:240] + "..."
		}
		return CertificationCheck{Status: certFail, Detail: detail, LatencyMS: result.Duration.Milliseconds(), HTTPStatus: result.Status}
	}
	return CertificationCheck{}
}

func responseIDFromBody(body []byte) string {
	var response struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &response)
	return response.ID
}

func certificationOverall(result ModelCertification) string {
	required := []CertificationCheck{result.Text, result.Streaming, result.Tools, result.ApplyPatch, result.MultiTurn}
	hasBlocked := false
	for _, check := range required {
		switch check.Status {
		case certFail:
			return certFail
		case certBlocked:
			hasBlocked = true
		}
	}
	if hasBlocked {
		return certBlocked
	}
	return certPass
}

func printModelCertification(w io.Writer, id string, result ModelCertification) {
	fmt.Fprintf(w, "\n%s -> %s/%s [%s]\n", id, result.Provider, result.UpstreamModel, strings.ToUpper(result.Overall))
	checks := []struct {
		name  string
		check CertificationCheck
	}{
		{"Text", result.Text}, {"Streaming", result.Streaming}, {"Tools", result.Tools},
		{"apply_patch", result.ApplyPatch}, {"MCP namespace", result.MCPNamespace},
		{"Vision", result.Vision}, {"Multi-turn", result.MultiTurn},
	}
	for _, row := range checks {
		detail := row.check.Detail
		if row.check.LatencyMS > 0 {
			detail = fmt.Sprintf("%s; %dms", detail, row.check.LatencyMS)
		}
		fmt.Fprintf(w, "  %-14s %-10s %s\n", row.name, strings.ToUpper(row.check.Status), detail)
	}
	if len(result.Reasoning) > 0 {
		keys := make([]string, 0, len(result.Reasoning))
		for key := range result.Reasoning {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			check := result.Reasoning[key]
			fmt.Fprintf(w, "  reasoning/%-4s %-10s %s\n", key, strings.ToUpper(check.Status), check.Detail)
		}
	}
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
