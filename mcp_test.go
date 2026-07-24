package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderMCPConfigUsesACCSubcommandsAndKeepsRawOsascriptOptIn(t *testing.T) {
	config, err := renderMCPConfig("/tmp/acc", false)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(config, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"acc-websearch", "acc-mac-control"} {
		server, ok := decoded.Servers[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if server.Command != "/tmp/acc" || len(server.Args) != 3 || server.Args[0] != "mcp" || server.Args[1] != "serve" {
			t.Fatalf("bad %s command: %+v", name, server)
		}
	}
	if _, ok := decoded.Servers["acc-osascript"]; ok {
		t.Fatal("raw osascript must be opt-in")
	}

	config, err = renderMCPConfig("/tmp/acc", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(config, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.Servers["acc-osascript"]; !ok {
		t.Fatal("raw osascript missing after opt-in")
	}
}

func TestInstallClaude3PMCPConfigReplacesLegacyServersAndPreservesSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	original := []byte(`{
  "mcpServers": {
    "websearch": {"command":"node","args":["old-web.js"]},
    "mac-control": {"command":"node","args":["old-mac.js"]},
    "osascript": {"command":"node","args":["old-osa.js"]},
    "tavily": {"command":"npx","env":{"TOKEN":"keep-me"}}
  },
  "deploymentMode": "3p",
  "preferences": {"menuBarEnabled": false}
}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}

	backup, err := installClaude3PMCPConfig(path, "/tmp/acc", true)
	if err != nil {
		t.Fatal(err)
	}
	backupData, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backupData) != string(original) {
		t.Fatal("Claude-3p backup did not preserve the original config")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["deploymentMode"]) != `"3p"` || !strings.Contains(string(decoded["preferences"]), "menuBarEnabled") {
		t.Fatalf("Claude-3p settings were lost: %s", data)
	}
	var servers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(decoded["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"websearch", "mac-control", "osascript"} {
		if _, ok := servers[legacy]; ok {
			t.Fatalf("legacy server %s was not removed", legacy)
		}
	}
	for _, name := range []string{"acc-websearch", "acc-mac-control", "acc-osascript"} {
		server, ok := servers[name]
		if !ok || server.Command != "/tmp/acc" {
			t.Fatalf("ACC server %s missing or invalid: %+v", name, server)
		}
	}
	if servers["tavily"].Command != "npx" || servers["tavily"].Env["TOKEN"] != "keep-me" {
		t.Fatalf("unrelated MCP server was changed: %+v", servers["tavily"])
	}
}

func TestFetchWebPageHandlesHTMLRedirectAndStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/article", http.StatusFound)
	})
	mux.HandleFunc("/article", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Test &amp; Page</title><style>bad</style></head><body><nav>menu</nav><article><h1>Heading</h1><p>Useful text.</p></article><script>bad()</script></body></html>`))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	page, err := fetchWebPage(context.Background(), server.URL+"/redirect", webFetchOptions{
		AllowPrivate: true,
		Client:       server.Client(),
		Timeout:      time.Second,
		MaxChars:     10_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Status != http.StatusOK || page.Title != "Test & Page" || !strings.Contains(page.Text, "Useful text.") {
		t.Fatalf("unexpected page: %+v", page)
	}
	if strings.Contains(page.Text, "menu") || strings.Contains(page.Text, "bad()") {
		t.Fatalf("page chrome leaked into readable text: %q", page.Text)
	}

	_, err = fetchWebPage(context.Background(), server.URL+"/missing", webFetchOptions{
		AllowPrivate: true,
		Client:       server.Client(),
		Timeout:      time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected clear 404 error, got %v", err)
	}
}

func TestFetchWebPageRejectsPrivateHostsByDefault(t *testing.T) {
	_, err := fetchWebPage(context.Background(), "http://127.0.0.1/private", webFetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected private-host rejection, got %v", err)
	}
}

func TestNormalizeNotesFolderPathAndRecentCounts(t *testing.T) {
	got, err := normalizeNotesFolderPath("  Stillness // Dreams / ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Stillness/Dreams" {
		t.Fatalf("folder path = %q", got)
	}
	for _, count := range []int{1, 3, 7} {
		if err := validateRecentCount(count); err != nil {
			t.Fatalf("count %d rejected: %v", count, err)
		}
	}
	if err := validateRecentCount(5); err == nil {
		t.Fatal("count 5 should be rejected")
	}
}

func TestMCPServerListsNamedTools(t *testing.T) {
	server := newWebsearchMCPServer()
	response := server.handle(mcpRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"})
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, name := range []string{"web_search", "web_fetch"} {
		if !strings.Contains(text, name) {
			t.Fatalf("tools/list missing %s: %s", name, text)
		}
	}
}

func TestMacControlListsPathBasedAndGuardedNotesTools(t *testing.T) {
	server := newMacControlMCPServer()
	names := map[string]bool{}
	for _, tool := range server.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"notes_folders", "notes_folder_guide", "notes_recent", "notes_get", "notes_append", "notes_replace", "notes_delete"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
	if _, err := notesReplace(context.Background(), map[string]any{"id": "x", "text": "new", "confirm": false}); err == nil {
		t.Fatal("notes_replace must require confirmation")
	}
	if _, err := notesDelete(context.Background(), map[string]any{"id": "x", "confirm": false}); err == nil {
		t.Fatal("notes_delete must require confirmation")
	}
}

func TestNotesHTMLConversionEscapesText(t *testing.T) {
	got := textToNotesHTML("one < two\n\nthree & four")
	want := "<div>one &lt; two</div><div><br></div><div>three &amp; four</div>"
	if got != want {
		t.Fatalf("HTML = %q, want %q", got, want)
	}
}

func TestBuildNoteMutationJXAPreservesTitleOnReplace(t *testing.T) {
	script := buildNoteMutationJXA("id", "title", "Notes", "On My Mac", "<div>replacement</div>", true)
	for _, expected := range []string{
		"const originalTitle = safeName(target.note);",
		"target.note.body = '<div>' + escapeHTML(originalTitle) + '</div>' + fragment;",
		"result.titlePreserved = result.title === originalTitle;",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("replacement script does not preserve and verify the title; missing %q", expected)
		}
	}
}

func TestClaudeArgsAddMCPConfigWithoutOverridingExplicitChoice(t *testing.T) {
	got := claudeArgsWithMCP([]string{"--model", "sonnet"}, "/tmp/mcp.json")
	want := []string{"--model", "sonnet", "--strict-mcp-config", "--mcp-config", "/tmp/mcp.json"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args = %q, want %q", got, want)
	}
	explicit := []string{"--mcp-config", "/custom.json"}
	got = claudeArgsWithMCP(explicit, "/tmp/mcp.json")
	want = []string{"--mcp-config", "/custom.json", "--strict-mcp-config"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("explicit MCP config was overwritten: %q", got)
	}
	explicit = []string{"--strict-mcp-config", "--mcp-config=/custom.json"}
	got = claudeArgsWithMCP(explicit, "/tmp/mcp.json")
	if strings.Join(got, "|") != strings.Join(explicit, "|") {
		t.Fatalf("strict MCP config flags were duplicated: %q", got)
	}
}

func TestMCPStdioInitializeAndList(t *testing.T) {
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2024-11-05\"}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	var output bytes.Buffer
	if err := runMCPStdio(newWebsearchMCPServer(), input, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"acc-websearch", "web_search", "web_fetch"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("stdio output missing %q: %s", expected, text)
		}
	}
}
