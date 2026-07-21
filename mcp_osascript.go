package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	maxOsascriptSource = 100_000
	defaultScriptTime  = 30 * time.Second
)

func newOsascriptMCPServer() *mcpServer {
	server := &mcpServer{
		Name: "acc-osascript", Version: "3.0.0",
		Tools: []mcpTool{{
			Name:        "osascript",
			Description: "Advanced unrestricted Mac automation. Run AppleScript or JXA through /usr/bin/osascript. This can control apps and modify local data, so use it only when a safer named mac-control tool cannot do the job.",
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false},
			InputSchema: objectSchema(map[string]any{
				"script":   map[string]any{"type": "string", "maxLength": maxOsascriptSource, "description": "AppleScript or JXA source"},
				"language": map[string]any{"type": "string", "enum": []string{"applescript", "javascript"}, "description": "Default: applescript"},
			}, []string{"script"}),
		}},
		Handlers: map[string]mcpToolHandler{},
	}
	server.Handlers["osascript"] = func(ctx context.Context, args map[string]any) (any, error) {
		source, err := requiredString(args, "script")
		if err != nil {
			return nil, err
		}
		if len(source) > maxOsascriptSource {
			return nil, fmt.Errorf("script exceeds %d characters", maxOsascriptSource)
		}
		language := optionalString(args, "language")
		if language == "" {
			language = "applescript"
		}
		return runOsascript(ctx, source, language)
	}
	return server
}

func runOsascript(ctx context.Context, source, language string) (map[string]any, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("osascript is available only on macOS")
	}
	args := []string{}
	switch strings.ToLower(language) {
	case "applescript":
	case "javascript", "jxa":
		args = append(args, "-l", "JavaScript")
	default:
		return nil, fmt.Errorf("language must be applescript or javascript")
	}
	args = append(args, "-e", source)
	ctx, cancel := context.WithTimeout(ctx, defaultScriptTime)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/osascript", args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("osascript timed out after %s", defaultScriptTime)
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("osascript failed: %s", message)
	}
	return map[string]any{
		"stdout":   strings.TrimSpace(stdout.String()),
		"stderr":   strings.TrimSpace(stderr.String()),
		"language": language,
	}, nil
}
