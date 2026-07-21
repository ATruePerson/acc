package main

import (
	"strings"
	"testing"
)

func TestVirtualWorkflowRequiresReadsBeforeWriteAndPassesAfterFix(t *testing.T) {
	files := map[string]string{
		"AGENTS.md":    "rules",
		"calc.go":      "return a - b",
		"calc_test.go": "want 5",
	}
	reads := map[string]bool{}
	writes := 0
	result := RunResult{}

	_, valid, repair := executeVirtual("workflow", responseItem{Type: "function_call", Name: "write_file", Arguments: `{"path":"calc.go","content":"return a + b"}`}, files, reads, &writes, &result)
	if valid || !repair || writes != 0 {
		t.Fatal("write before inspection must be rejected as repairable")
	}
	for _, path := range []string{"AGENTS.md", "calc.go", "calc_test.go"} {
		_, valid, _ = executeVirtual("workflow", responseItem{Type: "function_call", Name: "read_file", Arguments: `{"path":"` + path + `"}`}, files, reads, &writes, &result)
		if !valid {
			t.Fatalf("valid read rejected: %s", path)
		}
	}
	_, valid, _ = executeVirtual("workflow", responseItem{Type: "function_call", Name: "write_file", Arguments: `{"path":"calc.go","content":"func Add(a,b int) int { return a + b }"}`}, files, reads, &writes, &result)
	if !valid || writes != 1 {
		t.Fatal("valid inspected write was rejected")
	}
	output, valid, _ := executeVirtual("workflow", responseItem{Type: "function_call", Name: "run_tests", Arguments: `{}`}, files, reads, &writes, &result)
	if !valid || output != "PASS" || !result.TestsPassed {
		t.Fatalf("fixed virtual repo did not pass: %q", output)
	}
}

func TestCustomToolRequiresRawStringAndLongPromptKeepsMarkers(t *testing.T) {
	result := RunResult{}
	_, valid, _ := executeVirtual("custom_tool", responseItem{Type: "custom_tool_call", Name: "exec", Input: "2+2"}, nil, nil, new(int), &result)
	if !valid {
		t.Fatal("valid raw custom tool input rejected")
	}
	prompt := longPrompt(20000)
	if len(prompt) < 79000 || !containsAll(prompt, "ALPHA-7319", "OMEGA-2846", "NO_DESTRUCTIVE_ACTION") {
		t.Fatal("long-context fixture lost its constraints")
	}
}

func TestWorkflowFinalAcceptsTruthfulReportNotMagicWord(t *testing.T) {
	if !workflowFinalAccurate("Fixed Add with one line. All tests passed.") {
		t.Fatal("truthful verified workflow report was rejected")
	}
	if workflowFinalAccurate("Fixed it; tests were not run.") {
		t.Fatal("unverified workflow report was accepted")
	}
}

func containsAll(s string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(s, value) {
			return false
		}
	}
	return true
}
