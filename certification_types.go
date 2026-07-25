package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const certificationReportVersion = 2

const (
	certPass    = "pass"
	certFail    = "fail"
	certSkip    = "skip"
	certBlocked = "blocked"
)

type CertificationCheck struct {
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type ModelCertification struct {
	DisplayName   string                        `json:"display_name,omitempty"`
	Provider      string                        `json:"provider,omitempty"`
	UpstreamModel string                        `json:"upstream_model,omitempty"`
	CheckedAt     time.Time                     `json:"checked_at"`
	Overall       string                        `json:"overall"`
	Text          CertificationCheck            `json:"text"`
	Streaming     CertificationCheck            `json:"streaming"`
	Tools         CertificationCheck            `json:"tools"`
	ApplyPatch    CertificationCheck            `json:"apply_patch"`
	MCPNamespace  CertificationCheck            `json:"mcp_namespace"`
	Vision        CertificationCheck            `json:"vision"`
	MultiTurn     CertificationCheck            `json:"multi_turn"`
	Reasoning     map[string]CertificationCheck `json:"reasoning,omitempty"`
}

type CertificationReport struct {
	Version           int                           `json:"version"`
	GeneratedAt       time.Time                     `json:"generated_at"`
	Full              bool                          `json:"full"`
	ConfigFingerprint string                        `json:"config_fingerprint"`
	Models            map[string]ModelCertification `json:"models"`
}

func certificationPath() string {
	if configured := strings.TrimSpace(os.Getenv("ACC_CERTIFICATION_FILE")); configured != "" {
		return expandHome(configured)
	}
	return filepath.Join(accDir(), "certifications.json")
}

func certificationConfigFingerprint(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	type routeFingerprint struct {
		ID        string `json:"id"`
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		Tools     bool   `json:"tools"`
		Streaming bool   `json:"streaming"`
		Vision    bool   `json:"vision"`
	}
	models := codexNamedModels(cfg)
	values := make([]routeFingerprint, 0, len(models))
	for _, model := range models {
		values = append(values, routeFingerprint{
			ID: model.ID, Provider: model.Route.Provider, Model: model.Route.Model,
			Tools: model.Capability.ToolCallSupport, Streaming: model.Capability.StreamingSupport,
			Vision: model.Capability.ImageInputSupport,
		})
	}
	encoded, _ := json.Marshal(values)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func writePrivateFileAtomic(path string, data []byte) error {
	path = expandHome(path)
	if path == "" {
		return fmt.Errorf("empty output path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".acc-certification-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func loadCertificationReport(path string) (*CertificationReport, error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	var report CertificationReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	if report.Version != certificationReportVersion {
		return nil, fmt.Errorf("unsupported certification report version %d", report.Version)
	}
	return &report, nil
}

func applyCertificationPolicy(cfg *Config) {
	policy := strings.ToLower(strings.TrimSpace(os.Getenv("ACC_CERTIFICATION_POLICY")))
	if policy == "" {
		policy = "warn"
	}
	if policy == "off" || cfg == nil {
		return
	}
	report, err := loadCertificationReport(certificationPath())
	if err != nil {
		return
	}
	maxAge := 7 * 24 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("ACC_CERTIFICATION_MAX_AGE")); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr == nil && parsed > 0 {
			maxAge = parsed
		}
	}
	if time.Since(report.GeneratedAt) > maxAge {
		log.Printf("certification report ignored: older than %s", maxAge)
		return
	}
	if report.ConfigFingerprint != "" && report.ConfigFingerprint != certificationConfigFingerprintWithoutPolicy(cfg) {
		log.Printf("certification report ignored: Codex model catalog changed")
		return
	}
	for id, certification := range report.Models {
		capability, ok := cfg.Models[id]
		if !ok {
			continue
		}
		failures := []string{}
		if certification.Streaming.Status == certFail {
			failures = append(failures, "streaming")
			if policy == "strict" {
				capability.StreamingSupport = false
			}
		}
		if certification.Tools.Status == certFail || certification.ApplyPatch.Status == certFail {
			failures = append(failures, "tools/apply_patch")
			if policy == "strict" {
				capability.ToolCallSupport = false
			}
		}
		if certification.Vision.Status == certFail {
			failures = append(failures, "vision")
			if policy == "strict" {
				capability.ImageInputSupport = false
			}
		}
		if len(failures) > 0 {
			log.Printf("Codex model %s failed certified capabilities: %s (policy=%s)", id, strings.Join(failures, ", "), policy)
		}
		cfg.Models[id] = capability
	}
}

func certificationConfigFingerprintWithoutPolicy(cfg *Config) string {
	return certificationConfigFingerprint(cfg)
}
