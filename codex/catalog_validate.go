package codex

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateCatalog checks acc-models.json shape and slug uniqueness.
func ValidateCatalog(body []byte) (bool, string) {
	return validateCodexCatalog(body)
}

func validateCodexCatalog(body []byte) (bool, string) {
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if len(body) == 0 || json.Unmarshal(body, &catalog) != nil {
		return false, "missing or malformed JSON"
	}
	seenSlugs := map[string]bool{}
	seenModels := map[string]bool{}
	for _, model := range catalog.Models {
		lower := strings.ToLower(model.Slug)
		if seenSlugs[model.Slug] || lower == "opus" || lower == "sonnet" || lower == "haiku" {
			return false, "duplicate, ambiguous, or forbidden model ID: " + model.Slug
		}
		provider, upstreamModel, ok := DecodeCodexSlug(model.Slug)
		if !ok {
			return false, "malformed slug (must contain exactly one slash with valid encoding): " + model.Slug
		}
		if provider == "" || upstreamModel == "" {
			return false, "empty provider or upstream model in slug: " + model.Slug
		}
		modelKey := provider + "\x00" + upstreamModel
		if seenModels[modelKey] {
			return false, "duplicate encoded model (two different slugs decode to same provider/model): " + model.Slug
		}
		seenSlugs[model.Slug] = true
		seenModels[modelKey] = true
	}
	if len(seenSlugs) == 0 {
		return false, "catalog has no models"
	}
	return true, fmt.Sprintf("%d unique provider-prefixed models", len(seenSlugs))
}
