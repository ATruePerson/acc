package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"time"
)

func runCertifyCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("certify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath(), "path to config.json")
	modelsCSV := fs.String("model", "", "one exact provider/model ID or a comma-separated list")
	full := fs.Bool("full", false, "run MCP, vision, and every reasoning-effort probe")
	jsonOnly := fs.Bool("json", false, "print the report as JSON")
	writeReport := fs.Bool("write", true, "save certifications.json")
	timeout := fs.Duration("timeout", 90*time.Second, "timeout for each live probe")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	loadDotenv(defaultEnvPath())
	previousPolicy, hadPolicy := os.LookupEnv("ACC_CERTIFICATION_POLICY")
	_ = os.Setenv("ACC_CERTIFICATION_POLICY", "off")
	cfg, err := loadConfig(*configPath)
	if hadPolicy {
		_ = os.Setenv("ACC_CERTIFICATION_POLICY", previousPolicy)
	} else {
		_ = os.Unsetenv("ACC_CERTIFICATION_POLICY")
	}
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(stderr, "validate config: %v\n", err)
		return 1
	}

	auth, authErr := newDefaultAuthManager()
	if authErr != nil {
		fmt.Fprintf(stderr, "auth warning: %v\n", authErr)
	}
	catalog := codexNamedModelsWithAuth(cfg, auth)
	catalogByID := make(map[string]codexNamedModel, len(catalog))
	modelIDs := make([]string, 0, len(catalog))
	for _, model := range catalog {
		catalogByID[model.ID] = model
		modelIDs = append(modelIDs, model.ID)
	}
	if strings.TrimSpace(*modelsCSV) != "" {
		modelIDs = nil
		seen := map[string]bool{}
		for _, id := range strings.Split(*modelsCSV, ",") {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			if _, ok := catalogByID[id]; !ok {
				fmt.Fprintf(stderr, "unknown Codex catalog model %q\n", id)
				return 2
			}
			modelIDs = append(modelIDs, id)
			seen[id] = true
		}
	}
	if len(modelIDs) == 0 {
		fmt.Fprintln(stderr, "no Codex catalog models selected")
		return 2
	}
	sort.Strings(modelIDs)

	s := &server{http: newUpstreamHTTPClient(), limiter: newProviderRateLimiter(cfg), auth: auth}
	s.cfg.Store(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", s.handleResponses)
	local := httptest.NewServer(mux)
	defer local.Close()

	client := &http.Client{Timeout: *timeout}
	report := CertificationReport{
		Version:           certificationReportVersion,
		GeneratedAt:       time.Now().UTC(),
		Full:              *full,
		ConfigFingerprint: certificationConfigFingerprint(cfg),
		Models:            make(map[string]ModelCertification, len(modelIDs)),
	}

	for _, id := range modelIDs {
		model := catalogByID[id]
		result := certifyOneModel(client, local.URL+"/v1/responses", model, *full)
		report.Models[id] = result
		if !*jsonOnly {
			printModelCertification(stdout, id, result)
		}
	}

	encoded, _ := json.MarshalIndent(report, "", "  ")
	encoded = append(encoded, '\n')
	if *writeReport {
		path := certificationPath()
		if err := writePrivateFileAtomic(path, encoded); err != nil {
			fmt.Fprintf(stderr, "write certification report: %v\n", err)
			return 1
		}
		if !*jsonOnly {
			fmt.Fprintf(stdout, "\nSaved: %s\n", path)
		}
	}
	if *jsonOnly {
		_, _ = stdout.Write(encoded)
	}
	return 0
}

func certifyOneModel(client *http.Client, endpoint string, model codexNamedModel, full bool) ModelCertification {
	id := model.ID
	capability := model.Capability
	result := ModelCertification{
		DisplayName:   model.Display,
		Provider:      model.Route.Provider,
		UpstreamModel: model.Route.Model,
		CheckedAt:     time.Now().UTC(),
		Reasoning:     map[string]CertificationCheck{},
	}
	base := map[string]any{
		"model":             id,
		"input":             "Reply with exactly ACC_CERTIFY_OK.",
		"max_output_tokens": 64,
		"store":             true,
	}
	text := certifyPost(client, endpoint, base)
	result.Text = certifyCompletedResponse(text, "basic response")
	responseID := responseIDFromBody(text.Body)

	streamPayload := cloneMap(base)
	streamPayload["stream"] = true
	streamPayload["store"] = false
	result.Streaming = certifyStreamingResponse(certifyPost(client, endpoint, streamPayload))

	if capability.ToolCallSupport {
		toolPayload := map[string]any{
			"model": id,
			"input": "Call the acc_certify_echo tool exactly once with value ACC_CERTIFY_OK. Do not answer normally.",
			"tools": []any{map[string]any{
				"type": "function", "name": "acc_certify_echo", "description": "Return the supplied value.",
				"parameters": map[string]any{
					"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
					"required": []string{"value"}, "additionalProperties": false,
				},
			}},
			"max_output_tokens": 128,
			"store":             false,
		}
		result.Tools = certifyOutputItem(certifyPost(client, endpoint, toolPayload), "function_call", "acc_certify_echo", "")

		patchPayload := map[string]any{
			"model": id,
			"input": "Use apply_patch to replace OLD with NEW in demo.txt. Return only the tool call.",
			"tools": []any{map[string]any{
				"type": "custom", "name": "apply_patch",
				"description": "Apply a patch. Input must be the raw patch text.",
			}},
			"max_output_tokens": 256,
			"store":             false,
		}
		result.ApplyPatch = certifyOutputItem(certifyPost(client, endpoint, patchPayload), "custom_tool_call", "apply_patch", "")
	} else {
		result.Tools = CertificationCheck{Status: certSkip, Detail: "disabled by selected model capability"}
		result.ApplyPatch = CertificationCheck{Status: certSkip, Detail: "tool calls disabled"}
	}

	if responseID != "" {
		continuation := map[string]any{
			"model":                id,
			"previous_response_id": responseID,
			"input":                "Reply with exactly ACC_CONTINUATION_OK.",
			"max_output_tokens":    64,
			"store":                false,
		}
		result.MultiTurn = certifyCompletedResponse(certifyPost(client, endpoint, continuation), "previous_response_id continuation")
	} else {
		result.MultiTurn = CertificationCheck{Status: certSkip, Detail: "initial response ID unavailable"}
	}

	if full {
		if capability.ToolCallSupport {
			namespacePayload := map[string]any{
				"model": id,
				"input": "Call acc_certify.echo exactly once with value ACC_CERTIFY_OK. Do not answer normally.",
				"tools": []any{map[string]any{
					"type": "namespace", "name": "acc_certify", "description": "Certification namespace.",
					"tools": []any{map[string]any{
						"type": "function", "name": "echo", "description": "Return the value.",
						"parameters": map[string]any{
							"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
							"required": []string{"value"}, "additionalProperties": false,
						},
					}},
				}},
				"max_output_tokens": 128,
				"store":             false,
			}
			result.MCPNamespace = certifyOutputItem(certifyPost(client, endpoint, namespacePayload), "function_call", "echo", "acc_certify")
		} else {
			result.MCPNamespace = CertificationCheck{Status: certSkip, Detail: "tool calls disabled"}
		}

		if capability.ImageInputSupport {
			visionPayload := map[string]any{
				"model": id,
				"input": []any{map[string]any{
					"type": "message", "role": "user",
					"content": []any{
						map[string]any{"type": "input_text", "text": "Reply with exactly ACC_IMAGE_OK."},
						map[string]any{"type": "input_image", "image_url": onePixelPNGDataURL},
					},
				}},
				"max_output_tokens": 64,
				"store":             false,
			}
			result.Vision = certifyCompletedResponse(certifyPost(client, endpoint, visionPayload), "image input")
		} else {
			result.Vision = CertificationCheck{Status: certSkip, Detail: "disabled by selected model capability"}
		}

		for _, effort := range supportedEfforts(capability) {
			payload := cloneMap(base)
			payload["store"] = false
			payload["reasoning"] = map[string]any{"effort": effort}
			result.Reasoning[effort] = certifyCompletedResponse(certifyPost(client, endpoint, payload), "reasoning effort "+effort)
		}
	} else {
		result.MCPNamespace = CertificationCheck{Status: certSkip, Detail: "run with --full"}
		result.Vision = CertificationCheck{Status: certSkip, Detail: "run with --full"}
	}

	result.Overall = certificationOverall(result)
	return result
}

const onePixelPNGDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2nE4AAAAASUVORK5CYII="
