package codex

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type codexRoutingState struct {
	Mode                  string
	Endpoint              string
	Provider              string
	Catalog               string
	Model                 string
	RootBaseURL           string
	ActiveACC             bool
	ActiveOpenCodex       bool
	ActiveCustomRouting   bool
	ProviderPrefixedModel bool
}

type codexTOMLBlock struct {
	Name  string
	Lines []string
}

func splitCodexLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := make([]string, 0, strings.Count(text, "\n")+1)
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
	}
	return lines
}

func splitCodexTOMLBlocks(text string) []codexTOMLBlock {
	blocks := []codexTOMLBlock{{}}
	for _, line := range splitCodexLines(text) {
		if name, ok := codexTableHeader(line); ok {
			blocks = append(blocks, codexTOMLBlock{Name: name, Lines: []string{line}})
			continue
		}
		blocks[len(blocks)-1].Lines = append(blocks[len(blocks)-1].Lines, line)
	}
	return blocks
}

func codexTableHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "[[") || !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	end := strings.Index(trimmed, "]")
	if end <= 1 {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[end+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false
	}
	return strings.TrimSpace(trimmed[1:end]), true
}

func codexAssignment(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	quote := byte(0)
	escaped := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '=' {
			key = strings.TrimSpace(trimmed[:i])
			value = stripCodexInlineComment(strings.TrimSpace(trimmed[i+1:]))
			if key == "" {
				return "", "", false
			}
			return unquoteCodexKey(key), parseCodexScalar(value), true
		}
	}
	return "", "", false
}

func stripCodexInlineComment(value string) string {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '#' {
			return strings.TrimSpace(value[:i])
		}
	}
	return strings.TrimSpace(value)
}

func unquoteCodexKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) >= 2 && ((key[0] == '"' && key[len(key)-1] == '"') || (key[0] == '\'' && key[len(key)-1] == '\'')) {
		return parseCodexScalar(key)
	}
	return key
}

func parseCodexScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

func codexProviderName(table string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(table))
	const prefix = "model_providers."
	if !strings.HasPrefix(lower, prefix) {
		return "", false
	}
	name := strings.TrimSpace(table[len(prefix):])
	return strings.ToLower(unquoteCodexKey(name)), name != ""
}

func codexManagedProviderBlock(block codexTOMLBlock) bool {
	provider, ok := codexProviderName(block.Name)
	if !ok {
		return false
	}
	if provider == "acc" || strings.Contains(provider, "opencodex") {
		return true
	}
	for _, line := range block.Lines[1:] {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "# Auto-injected by opencodex") {
			return true
		}
		key, value, ok := codexAssignment(line)
		if !ok {
			continue
		}
		if key == "base_url" || key == "openai_base_url" {
			if codexURLUsesPort(value, "10100") || codexURLUsesPort(value, "9999") {
				return true
			}
		}
	}
	return false
}

func sanitizeCodexSubscriptionConfig(original string) string {
	return sanitizeCodexConfig(original, false, true)
}

func sanitizeCodexConfig(original string, removeWebSearch bool, stripRootACCBlock bool) string {
	blocks := splitCodexTOMLBlocks(original)
	var out strings.Builder
	for index, block := range blocks {
		if index > 0 && codexManagedProviderBlock(block) {
			continue
		}
		if index > 0 {
			for _, line := range block.Lines {
				if strings.EqualFold(strings.TrimSpace(line), "# Auto-injected by opencodex") {
					continue
				}
				out.WriteString(line)
			}
			continue
		}
		inOwned := false
		skipOwned := stripRootACCBlock
		for _, line := range block.Lines {
			trimmed := strings.TrimSpace(line)
			if skipOwned {
				switch trimmed {
				case accCodexRootBegin:
					inOwned = true
					continue
				case accCodexRootEnd:
					inOwned = false
					continue
				}
				if inOwned {
					continue
				}
			}
			switch trimmed {
			case accCodexRootBegin:
				inOwned = true
				out.WriteString(line)
				continue
			case accCodexRootEnd:
				inOwned = false
				out.WriteString(line)
				continue
			}
			if trimmed == accCodexProvider || strings.EqualFold(trimmed, "# Auto-injected by opencodex") {
				continue
			}
			if inOwned {
				// Inside ACC-owned section: keep the line as-is
				out.WriteString(line)
				continue
			}
			// Outside ACC-owned section: check if we should remove this line
			key, _, ok := codexAssignment(line)
			if ok {
				switch key {
				case "model", "model_provider", "model_catalog_json", "openai_base_url", "model_reasoning_effort":
					continue
				case "web_search":
					if removeWebSearch {
						continue
					}
				}
			}
			out.WriteString(line)
		}
	}
	return out.String()
}

func codexNewline(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func renderCodexACCConfig(original, catalogPath, baseURL, model, effort string) string {
	// ACC temporarily owns web_search while active. The durable subscription
	// baseline keeps the user's original value and restore puts it back.
	sanitized := sanitizeCodexConfig(original, true, true)
	newline := codexNewline(original)
	blocks := splitCodexTOMLBlocks(sanitized)
	root := ""
	rest := ""
	if len(blocks) > 0 {
		root = strings.Join(blocks[0].Lines, "")
		for _, block := range blocks[1:] {
			rest += strings.Join(block.Lines, "")
		}
	}
	root = strings.TrimRight(root, "\r\n")
	var out strings.Builder
	if root != "" {
		out.WriteString(root)
		out.WriteString(newline)
	}
	out.WriteString(accCodexRootBegin + newline)
	out.WriteString("model = " + strconv.Quote(model) + newline)
	if effort != "" {
		out.WriteString("model_reasoning_effort = " + strconv.Quote(effort) + newline)
	}
	out.WriteString(`model_provider = "acc"` + newline)
	out.WriteString("model_catalog_json = " + strconv.Quote(catalogPath) + newline)
	out.WriteString(`web_search = "disabled"` + newline)
	out.WriteString(accCodexRootEnd + newline + newline)
	out.WriteString(strings.TrimLeft(rest, "\r\n"))
	if rest != "" && !strings.HasSuffix(out.String(), newline) {
		out.WriteString(newline)
	}
	if rest != "" {
		out.WriteString(newline)
	}
	out.WriteString(accCodexProvider + newline)
	out.WriteString("[model_providers.acc]" + newline)
	out.WriteString(`name = "ACC"` + newline)
	out.WriteString("base_url = " + strconv.Quote(strings.TrimRight(baseURL, "/")) + newline)
	out.WriteString(`wire_api = "responses"` + newline)
	out.WriteString("requires_openai_auth = true" + newline)
	out.WriteString("supports_websockets = false" + newline)
	return out.String()
}

func inspectCodexRouting(config string) codexRoutingState {
	state := codexRoutingState{Mode: "Subscription", Endpoint: "Built-in OpenAI subscription", Provider: "OpenAI subscription", Catalog: "Built-in"}
	blocks := splitCodexTOMLBlocks(config)
	root := map[string]string{}
	providers := map[string]map[string]string{}
	if len(blocks) > 0 {
		for _, line := range blocks[0].Lines {
			key, value, ok := codexAssignment(line)
			if ok {
				root[key] = value
			}
		}
	}
	for _, block := range blocks[1:] {
		provider, ok := codexProviderName(block.Name)
		if !ok {
			continue
		}
		values := map[string]string{}
		for _, line := range block.Lines[1:] {
			key, value, ok := codexAssignment(line)
			if ok {
				values[key] = value
			}
		}
		providers[provider] = values
	}

	state.Model = root["model"]
	state.Provider = root["model_provider"]
	state.Catalog = root["model_catalog_json"]
	state.RootBaseURL = root["openai_base_url"]
	if state.Provider == "" {
		state.Provider = "OpenAI subscription"
	}
	if state.Catalog == "" {
		state.Catalog = "Built-in"
	}
	endpoint := state.RootBaseURL
	selectedProvider := strings.ToLower(root["model_provider"])
	if endpoint == "" && selectedProvider != "" {
		if provider := providers[selectedProvider]; provider != nil {
			endpoint = provider["base_url"]
			if endpoint == "" {
				endpoint = provider["openai_base_url"]
			}
		}
	}
	if endpoint != "" {
		state.Endpoint = endpoint
	}
	state.ProviderPrefixedModel = strings.Contains(state.Model, "/")
	state.ActiveOpenCodex = strings.Contains(selectedProvider, "opencodex") || codexURLUsesPort(endpoint, "10100") || strings.Contains(strings.ToLower(filepath.Base(root["model_catalog_json"])), "opencodex")
	state.ActiveACC = selectedProvider == "acc" || codexURLUsesPort(endpoint, "9999")
	state.ActiveCustomRouting = root["model_provider"] != "" || root["model_catalog_json"] != "" || root["openai_base_url"] != "" || state.ProviderPrefixedModel
	switch {
	case state.ActiveOpenCodex:
		state.Mode = "OpenCodex"
	case state.ActiveACC:
		state.Mode = "ACC"
	case state.ActiveCustomRouting:
		if strings.EqualFold(root["model_provider"], "openai") && root["model_catalog_json"] == "" && root["openai_base_url"] == "" && !state.ProviderPrefixedModel {
			state.Mode = "Subscription"
		} else {
			state.Mode = "Unknown"
		}
	default:
		state.Mode = "Subscription"
	}
	return state
}

func validateSubscriptionCodexConfig(config string) error {
	if err := validateCodexConfigText(config); err != nil {
		return err
	}
	routing := inspectCodexRouting(config)
	if routing.Mode != "Subscription" {
		return fmt.Errorf("mode is %s", routing.Mode)
	}
	if len(splitCodexTOMLBlocks(config)) > 0 {
		for _, line := range splitCodexTOMLBlocks(config)[0].Lines {
			key, _, ok := codexAssignment(line)
			if !ok {
				continue
			}
			switch key {
			case "model", "model_provider", "model_catalog_json", "openai_base_url":
				return fmt.Errorf("custom root routing key %q remains", key)
			}
		}
	}
	for _, block := range splitCodexTOMLBlocks(config) {
		if codexManagedProviderBlock(block) {
			return fmt.Errorf("managed provider block %q remains", block.Name)
		}
	}
	return nil
}

func codexURLUsesPort(raw, port string) bool {
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Port() == port {
		return true
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, ":"+port+"/") || strings.HasSuffix(lower, ":"+port)
}

func validateCodexLoopbackBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" || parsed.User != nil {
		return fmt.Errorf("ACC Codex base URL must be unauthenticated HTTP loopback")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("ACC Codex base URL is not loopback-only")
	}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1") {
		return fmt.Errorf("ACC Codex base URL must end in /v1")
	}
	return nil
}

func activeCodexCatalogPath(config, configPath string) string {
	blocks := splitCodexTOMLBlocks(config)
	if len(blocks) == 0 {
		return ""
	}
	for _, line := range blocks[0].Lines {
		key, value, ok := codexAssignment(line)
		if ok && key == "model_catalog_json" {
			return resolveCodexPath(value, configPath)
		}
	}
	return ""
}

func resolveCodexPath(path, configPath string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	return filepath.Clean(path)
}

func catalogHasCodexModel(body []byte, model string) bool {
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &catalog) != nil {
		return false
	}
	for _, entry := range catalog.Models {
		if entry.Slug == model {
			return true
		}
	}
	return false
}

func knownManagedCodexCatalog(path, accCatalogPath string) bool {
	clean := filepath.Clean(path)
	if clean == filepath.Clean(accCatalogPath) {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(clean))
	if !strings.Contains(base, "opencodex") {
		return false
	}
	for _, root := range []string{
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".opencodex"),
		filepath.Join(home, ".config", "opencodex"),
	} {
		rel, err := filepath.Rel(filepath.Clean(root), clean)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return true
		}
	}
	return false
}
