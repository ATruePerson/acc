package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "embed"
)

const (
	accPersonaStart = "<acc_persona>"
	accPersonaEnd   = "</acc_persona>"
)

type personaRuntime string

const (
	personaRuntimeCodex      personaRuntime = "codex"
	personaRuntimeClaudeCode personaRuntime = "claude-code"
	personaRuntimeGeneric    personaRuntime = "generic"
)

//go:embed persona.md
var embeddedPersonaMarkdown string

var (
	personaFileMu   sync.Mutex
	personaFilePath string
)

// setPersonaFilePath points ACC persona loading at an editable markdown file.
// Empty keeps the embedded fallback.
func setPersonaFilePath(path string) {
	personaFileMu.Lock()
	defer personaFileMu.Unlock()
	personaFilePath = path
}

func personaMarkdownSource() string {
	personaFileMu.Lock()
	path := personaFilePath
	personaFileMu.Unlock()
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return embeddedPersonaMarkdown
}

// accPersona assembles ACC's three small prompt sections. Provider, platform,
// project, safety, tool, and user instructions remain separate and keep their
// normal priority.
func accPersona(provider, model string) string {
	return accPersonaForRuntime(provider, model, personaRuntimeCodex)
}

func accPersonaForRuntime(provider, model string, runtime personaRuntime) string {
	backend := backendLabel(provider, model)
	if backend == "" {
		backend = "the backend selected by ACC for this request"
	}
	core, adapter, personal := parsePersonaMarkdown(personaMarkdownSource(), runtime)
	core = strings.ReplaceAll(core, "{{backend}}", backend)
	return accPersonaStart + "\n" + core + "\n\n" + adapter + "\n\n" + personal + "\n" + accPersonaEnd
}

func parsePersonaMarkdown(src string, runtime personaRuntime) (core, adapter, personal string) {
	sections := splitMarkdownSections(src)
	core = strings.TrimSpace(sections["core behavior"])
	personal = strings.TrimSpace(sections["personal instructions"])
	switch runtime {
	case personaRuntimeClaudeCode:
		adapter = strings.TrimSpace(sections["runtime: claude-code"])
	case personaRuntimeGeneric:
		adapter = strings.TrimSpace(sections["runtime: generic"])
	default:
		adapter = strings.TrimSpace(sections["runtime: codex"])
	}
	return core, adapter, personal
}

func splitMarkdownSections(src string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(src, "\n")
	var current string
	var body strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		out[current] = strings.TrimSpace(body.String())
		body.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			current = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
			continue
		}
		if current != "" {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	flush()
	return out
}

func backendLabel(provider, model string) string {
	provider = strings.Trim(strings.TrimSpace(provider), "/")
	model = strings.Trim(strings.TrimSpace(model), "/")
	if provider == "" {
		return model
	}
	if model == "" || strings.EqualFold(model, provider) {
		return provider
	}
	if strings.HasPrefix(strings.ToLower(model), strings.ToLower(provider)+"/") {
		return model
	}
	return provider + "/" + model
}

// stripACCPersona removes only ACC's own marked prompt. It deliberately leaves
// Codex, provider, project, developer, and user instructions byte-for-byte.
func stripACCPersona(s string) string {
	for {
		start := strings.Index(s, accPersonaStart)
		if start < 0 {
			return s
		}
		endRel := strings.Index(s[start:], accPersonaEnd)
		if endRel < 0 {
			return s
		}
		end := start + endRel + len(accPersonaEnd)
		s = s[:start] + s[end:]
	}
}

func requestWithACCPersona(base *OpenAIRequest, route Route, runtimes ...personaRuntime) (*OpenAIRequest, error) {
	b, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var out OpenAIRequest
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	runtime := personaRuntimeCodex
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	persona := accPersonaForRuntime(route.Provider, route.Model, runtime)
	if len(out.Messages) > 0 && out.Messages[0].Role == "system" {
		original := stripACCPersona(decodeStringContent(out.Messages[0].Content))
		if strings.TrimSpace(original) != "" {
			persona += "\n\n" + original
		}
		out.Messages[0].Content = jsonString(persona)
	} else {
		out.Messages = append([]OpenAIMessage{{Role: "system", Content: jsonString(persona)}}, out.Messages...)
	}
	return &out, nil
}

// chatJSONWithACCPersona changes only the model and ACC-owned identity prompt
// in a Chat Completions body. Unknown provider-compatible fields stay intact.
func chatJSONWithACCPersona(raw []byte, route Route) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	request["model"] = route.Model
	persona := accPersonaForRuntime(route.Provider, route.Model, personaRuntimeGeneric)

	messages, _ := request["messages"].([]any)
	if len(messages) > 0 {
		if first, ok := messages[0].(map[string]any); ok && first["role"] == "system" {
			if content, ok := first["content"].(string); ok {
				original := stripACCPersona(content)
				if strings.TrimSpace(original) != "" {
					persona += "\n\n" + original
				}
				first["content"] = persona
				request["messages"] = messages
				return json.Marshal(request)
			}
		}
	}
	request["messages"] = append([]any{map[string]any{"role": "system", "content": persona}}, messages...)
	return json.Marshal(request)
}

func resolvePersonaFile(baseDir string) string {
	candidates := []string{
		filepath.Join(baseDir, "system_prompts", "persona.md"),
		filepath.Join("system_prompts", "persona.md"),
	}
	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(path)
			if err == nil {
				return abs
			}
			return path
		}
	}
	return ""
}

func resolveSystemPrepend(baseDir, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	content, err := loadPrependFile(baseDir, value[1:])
	if err != nil {
		return "", err
	}
	return content, nil
}

func loadPrependFile(baseDir, path string) (string, error) {
	original := path
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if !filepath.IsAbs(path) {
		candidates := []string{
			filepath.Join(baseDir, path),
			path,
		}
		found := false
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("failed to read system_prepend file %q: not found relative to %q", original, baseDir)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read system_prepend file %q: %w", original, err)
	}
	return string(content), nil
}
