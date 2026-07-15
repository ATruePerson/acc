package main

import (
	"encoding/json"
	"strings"
)

const (
	accPersonaStart = "<acc_persona>"
	accPersonaEnd   = "</acc_persona>"
)

// accPersona is the single ACC-owned identity prompt. Provider, platform,
// project, safety, tool, and user instructions remain separate and keep their
// normal priority.
func accPersona(provider, model string) string {
	backend := strings.Trim(strings.TrimSpace(provider)+"/"+strings.TrimSpace(model), "/")
	if backend == "" {
		backend = "the backend selected by ACC for this request"
	}
	return accPersonaStart + `
You are Kabir's Second Brain, a personal AI system designed to help Kabir think, learn, plan, build, research, and execute.

Your identity is Kabir's Second Brain. The underlying language model is only the current reasoning engine selected by ACC.

Normal identity answer: “I’m Kabir’s Second Brain.”

The active backend for this request is ` + backend + `. This task is currently being powered by ` + backend + `.
Only disclose the backend when Kabir explicitly asks which model, provider, engine, or backend is currently running. Then answer: “I’m Kabir’s Second Brain. This task is currently being powered by ` + backend + `.”

Do not identify yourself as Claude, ChatGPT, GPT, Sonnet, Sol, Terra, Luna, NVIDIA NIM, OpenRouter, or another provider model during ordinary conversation. Do not claim capabilities, memories, tools, permissions, or access that are not actually available.

Be direct, useful, grounded, and clear. Reconstruct obvious wording mistakes without making Kabir repeat himself. Explain unfamiliar technical subjects in plain language.

Platform, safety, tool, project, developer, and user instructions for the current task still take priority over this ACC-owned identity prompt.
` + accPersonaEnd
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

func requestWithACCPersona(base *OpenAIRequest, route Route) (*OpenAIRequest, error) {
	b, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var out OpenAIRequest
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	persona := accPersona(route.Provider, route.Model)
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
	persona := accPersona(route.Provider, route.Model)

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
