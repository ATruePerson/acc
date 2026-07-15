package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const mcpProtocolVersion = "2024-11-05"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC    string          `json:"jsonrpc"`
	ID         json.RawMessage `json:"id,omitempty"`
	Result     any             `json:"result,omitempty"`
	Error      *mcpRPCError    `json:"error,omitempty"`
	NoResponse bool            `json:"-"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type mcpToolHandler func(context.Context, map[string]any) (any, error)

type mcpServer struct {
	Name     string
	Version  string
	Tools    []mcpTool
	Handlers map[string]mcpToolHandler
}

func (s *mcpServer) handle(req mcpRequest) mcpResponse {
	return s.handleContext(context.Background(), req)
}

func (s *mcpServer) handleContext(ctx context.Context, req mcpRequest) mcpResponse {
	response := mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		protocol := mcpProtocolVersion
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &params) == nil && params.ProtocolVersion != "" {
			protocol = params.ProtocolVersion
		}
		response.Result = map[string]any{
			"protocolVersion": protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
	case "notifications/initialized":
		response.NoResponse = true
	case "ping":
		response.Result = map[string]any{}
	case "tools/list":
		response.Result = map[string]any{"tools": s.Tools}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			response.Error = &mcpRPCError{Code: -32602, Message: "invalid tool arguments: " + err.Error()}
			break
		}
		handler, ok := s.Handlers[params.Name]
		if !ok {
			response.Result = mcpCallError("unknown tool: " + params.Name)
			break
		}
		result, err := handler(ctx, params.Arguments)
		if err != nil {
			response.Result = mcpCallError(err.Error())
			break
		}
		text, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			response.Result = mcpCallError("encode result: " + err.Error())
			break
		}
		response.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
		}
	default:
		response.Error = &mcpRPCError{Code: -32601, Message: "method not found"}
	}
	return response
}

func mcpCallError(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": "ERROR: " + message}},
		"isError": true,
	}
}

func runMCPStdio(server *mcpServer, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 8<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request mcpRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			_ = encoder.Encode(mcpResponse{JSONRPC: "2.0", Error: &mcpRPCError{Code: -32700, Message: "parse error"}})
			continue
		}
		response := server.handleContext(context.Background(), request)
		if response.NoResponse {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

type mcpConfigServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func renderMCPConfig(executable string, includeRaw bool) ([]byte, error) {
	servers := map[string]mcpConfigServer{
		"acc-websearch": {
			Type: "stdio", Command: executable, Args: []string{"mcp", "serve", "websearch"},
		},
		"acc-mac-control": {
			Type: "stdio", Command: executable, Args: []string{"mcp", "serve", "mac-control"},
		},
	}
	if includeRaw {
		servers["acc-osascript"] = mcpConfigServer{
			Type: "stdio", Command: executable, Args: []string{"mcp", "serve", "osascript"},
		}
	}
	return json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
}

func defaultMCPConfigPath() string {
	return filepath.Join(accDir(), "mcp.json")
}

func writeMCPConfig(path, executable string, includeRaw bool) error {
	data, err := renderMCPConfig(executable, includeRaw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

func ensureMCPConfig(executable string) (string, error) {
	path := defaultMCPConfigPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return path, writeMCPConfig(path, executable, false)
}

func claudeArgsWithMCP(args []string, configPath string) []string {
	for i, arg := range args {
		if arg == "--mcp-config" || strings.HasPrefix(arg, "--mcp-config=") {
			return append([]string(nil), args...)
		}
		if i > 0 && args[i-1] == "--mcp-config" {
			return append([]string(nil), args...)
		}
	}
	out := []string{"--mcp-config", configPath}
	return append(out, args...)
}

func cmdMCP(args []string) {
	if len(args) == 0 {
		printMCPHelp()
		return
	}
	switch args[0] {
	case "serve":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: acc mcp serve <websearch|mac-control|osascript>")
			return
		}
		server, err := mcpServerByName(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if err := runMCPStdio(server, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "MCP server failed:", err)
		}
	case "install":
		flags := flag.NewFlagSet("acc mcp install", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		includeRaw := false
		flags.BoolVar(&includeRaw, "include-raw-osascript", false, "include unrestricted AppleScript/JXA execution")
		if err := flags.Parse(args[1:]); err != nil {
			fmt.Println("Usage: acc mcp install [--include-raw-osascript]")
			return
		}
		executable, err := os.Executable()
		if err != nil {
			fmt.Println("  Could not locate acc:", err)
			return
		}
		path := defaultMCPConfigPath()
		if err := writeMCPConfig(path, executable, includeRaw); err != nil {
			fmt.Println("  Could not write MCP config:", err)
			return
		}
		fmt.Printf("  Installed ACC MCP config at %s\n", path)
		fmt.Println("  Enabled: websearch, mac-control")
		if includeRaw {
			fmt.Println("  Enabled: raw osascript (unrestricted Mac automation)")
		} else {
			fmt.Println("  Raw osascript remains disabled. Re-run with --include-raw-osascript to enable it.")
		}
	case "doctor":
		cmdMCPDoctor()
	case "help", "--help", "-h":
		printMCPHelp()
	default:
		printMCPHelp()
	}
}

func cmdMCPDoctor() {
	fmt.Println("\n  acc mcp doctor")
	fmt.Printf("  %-14s OK (%d tools)\n", "websearch", len(newWebsearchMCPServer().Tools))
	if runtime.GOOS != "darwin" {
		fmt.Printf("  %-14s unavailable (macOS only)\n", "mac-control")
		fmt.Printf("  %-14s unavailable (macOS only)\n", "osascript")
	} else if _, err := exec.LookPath("osascript"); err != nil {
		fmt.Printf("  %-14s unavailable (/usr/bin/osascript not found)\n", "mac-control")
		fmt.Printf("  %-14s unavailable (/usr/bin/osascript not found)\n", "osascript")
	} else {
		fmt.Printf("  %-14s OK (%d tools)\n", "mac-control", len(newMacControlMCPServer().Tools))
		fmt.Printf("  %-14s OK (bundled, opt-in)\n", "osascript")
	}
	path := defaultMCPConfigPath()
	var config struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("  %-14s missing (run `acc mcp install`)\n\n", "config")
		return
	}
	if err := json.Unmarshal(data, &config); err != nil {
		fmt.Printf("  %-14s invalid: %v\n\n", "config", err)
		return
	}
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("  %-14s OK (%s)\n\n", "config", strings.Join(names, ", "))
}

func printMCPHelp() {
	fmt.Print(`acc mcp — bundled local tools

Usage:
  acc mcp install                         Install safe Claude MCP config
  acc mcp install --include-raw-osascript Also enable unrestricted AppleScript/JXA
  acc mcp doctor                          Check bundled tools and config
  acc mcp serve <name>                    Run one stdio MCP server
`)
}

func mcpServerByName(name string) (*mcpServer, error) {
	switch name {
	case "websearch":
		return newWebsearchMCPServer(), nil
	case "mac-control":
		return newMacControlMCPServer(), nil
	case "osascript":
		return newOsascriptMCPServer(), nil
	default:
		return nil, fmt.Errorf("unknown MCP server %q", name)
	}
}
