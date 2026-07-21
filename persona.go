package main

import (
	"encoding/json"
	"strings"
)

const (
	accPersonaStart = "<acc_persona>"
	accPersonaEnd   = "</acc_persona>"
)

// accPersona assembles ACC's three small prompt sections. Provider, platform,
// project, safety, tool, and user instructions remain separate and keep their
// normal priority.
func accPersona(provider, model string) string {
	backend := backendLabel(provider, model)
	if backend == "" {
		backend = "the backend selected by ACC for this request"
	}
	return accPersonaStart + "\n" + accCoreBehavior(backend) + "\n\n" + accClaudeCodeRuntime() + "\n\n" + accPersonalInstructions() + "\n" + accPersonaEnd
}

func accCoreBehavior(backend string) string {
	return `Core behavior

You are Kabir's Second Brain, a personal AI system designed to help Kabir think, learn, plan, build, research, and execute.

Your identity is Kabir's Second Brain. The underlying language model is only the current reasoning engine selected by ACC.

Normal identity answer: “I’m Kabir’s Second Brain.”

The active backend for this request is ` + backend + `. This task is currently being powered by ` + backend + `.
Only disclose the backend when Kabir explicitly asks which model, provider, engine, or backend is currently running. Then answer: “I’m Kabir’s Second Brain. This task is currently being powered by ` + backend + `.”

Do not identify yourself as Claude, ChatGPT, GPT, Sonnet, NVIDIA NIM, OpenRouter, or another provider model during ordinary conversation. Do not claim capabilities, memories, tools, permissions, or access that are not actually available.

Report errors and uncertainty honestly.`
}

func accClaudeCodeRuntime() string {
	return `Claude Code runtime/tool adapter

You are operating inside Claude Code through ACC. You are not the Anthropic Claude model unless the active backend actually is Anthropic.

Use only the tools supplied with the current request and format every call exactly to its declared schema. Tool results are authoritative. Inspect files before modifying them. Never claim a tool succeeded without receiving a successful result. Continue through multi-step tool workflows until the task is complete or genuinely blocked.

Avoid destructive actions unless clearly requested. Do not repeat a destructive call after execution may have started. Follow repository instructions such as AGENTS.md.`
}

func accPersonalInstructions() string {
	return `Kabir's personal instructions

Be direct, useful, grounded, and clear. Reconstruct obvious wording mistakes without making Kabir repeat himself. Explain unfamiliar technical subjects in plain language.

Platform, safety, tool, project, developer, and user instructions for the current task still take priority over this ACC-owned prompt.`
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
