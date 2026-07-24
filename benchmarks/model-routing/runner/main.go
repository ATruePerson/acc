package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Model struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Backend     string `json:"backend"`
	Reasoning   string `json:"reasoning"`
	Tools       bool   `json:"tools"`
	CustomTools bool   `json:"custom_tools"`
	Images      bool   `json:"images"`
}

type Case struct {
	ID, Category, Kind, Prompt, Expected string
	Runs                                 int
	ContextTokens                        int `json:"context_tokens"`
}

type responseItem struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	CallID    string          `json:"call_id"`
	Input     string          `json:"input"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
}

type responseBody struct {
	Status string
	Output []responseItem
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	}
	Error struct {
		Message string
	}
}

type RunResult struct {
	Timestamp                                  string `json:"timestamp"`
	Model, Provider                            string
	Backend, Reasoning                         string
	CaseID, Category                           string
	Run                                        int
	HTTPStatus                                 int    `json:"http_status"`
	Status                                     string `json:"status"`
	ErrorClass, Error                          string
	Correct                                    bool
	LatencyMS, TTFTMS                          int64
	InputTokens, OutputTokens                  int
	ToolCalls, InvalidToolArgs, RepairAttempts int
	ReadBeforeWrite, TestsRun, TestsPassed     bool
	RequestedModel, RequestedEffort            string
	ActualProvider, ActualModel, ActualEffort  string
	Fallback                                   bool
	Output                                     string
}

type runner struct {
	base, root string
	client     *http.Client
}

func main() {
	profile := flag.String("profile", "probe", "probe, core, or full")
	modelFilter := flag.String("models", "", "comma-separated model IDs")
	caseFilter := flag.String("cases", "", "comma-separated case IDs")
	runsOverride := flag.Int("runs", 0, "override repetitions per selected case")
	mergeFiles := flag.String("merge-files", "", "comma-separated prior results.json files to merge without new requests")
	base := flag.String("base-url", "http://127.0.0.1:9999/v1", "ACC API base")
	timeout := flag.Duration("timeout", 90*time.Second, "per-request timeout")
	flag.Parse()

	root, err := findRoot()
	must(err)
	if strings.TrimSpace(*mergeFiles) != "" {
		var combined []RunResult
		for _, path := range strings.Split(*mergeFiles, ",") {
			var document struct {
				Runs []RunResult `json:"runs"`
			}
			document = loadJSON[struct {
				Runs []RunResult `json:"runs"`
			}](strings.TrimSpace(path))
			combined = append(combined, document.Runs...)
		}
		writeSummary(root, "combined", combined)
		return
	}
	models := loadJSON[[]Model](filepath.Join(root, "cases", "models.json"))
	cases := loadJSON[[]Case](filepath.Join(root, "cases", "cases.json"))
	models = filterModels(models, *modelFilter)
	cases = filterCases(cases, *profile)
	cases = filterCaseIDs(cases, *caseFilter)
	if *runsOverride > 0 {
		for i := range cases {
			cases[i].Runs = *runsOverride
		}
	}

	r := &runner{base: strings.TrimRight(*base, "/"), root: root, client: &http.Client{Timeout: *timeout}}
	results := make([]RunResult, 0)
	for _, model := range models {
		for _, c := range cases {
			if !applicable(model, c) {
				continue
			}
			for run := 1; run <= c.Runs; run++ {
				fmt.Printf("%-43s %-26s %d/%d ... ", model.ID, c.ID, run, c.Runs)
				result := r.run(model, c, run)
				results = append(results, result)
				if result.Correct {
					fmt.Printf("PASS %dms\n", result.LatencyMS)
				} else {
					fmt.Printf("FAIL status=%d class=%s %s\n", result.HTTPStatus, result.ErrorClass, truncate(result.Error, 100))
				}
				writeRaw(root, result)
			}
		}
	}
	writeSummary(root, *profile, results)
}

func (r *runner) run(model Model, c Case, run int) RunResult {
	result := RunResult{Timestamp: time.Now().Format(time.RFC3339Nano), Model: model.ID, Provider: model.Provider, Backend: model.Backend, Reasoning: model.Reasoning, CaseID: c.ID, Category: c.Category, Run: run, RequestedModel: model.ID, RequestedEffort: "max"}
	var err error
	switch c.Kind {
	case "function_tool", "custom_tool", "workflow":
		result, err = r.runToolCase(result, model, c)
	case "stream":
		result, err = r.runStream(result, c)
	default:
		result, err = r.runText(result, c)
	}
	if err != nil {
		result.Error = err.Error()
		result.ErrorClass = classify(result.HTTPStatus, result.Error)
		if result.Status == "" {
			result.Status = "failed"
		}
	}
	return result
}

func (r *runner) runText(result RunResult, c Case) (RunResult, error) {
	prompt := c.Prompt
	input := any(prompt)
	if c.Kind == "long_context" {
		prompt = longPrompt(c.ContextTokens)
		input = prompt
	}
	if c.Kind == "image" {
		input = []any{map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": prompt},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"},
		}}}
	}
	payload := map[string]any{"model": result.Model, "input": input, "reasoning": map[string]any{"effort": "max"}, "max_output_tokens": 1024}
	body, headers, status, latency, err := r.post(payload)
	fillHTTP(&result, body, headers, status, latency)
	if err != nil {
		return result, err
	}
	result.Output = responseText(body.Output)
	result.Correct = exact(result.Output, c.Expected)
	if c.Kind == "image" {
		result.Correct = strings.EqualFold(strings.TrimSpace(result.Output), c.Expected)
	}
	if !result.Correct {
		return result, fmt.Errorf("incorrect final answer: %s", truncate(result.Output, 160))
	}
	return result, nil
}

func (r *runner) runStream(result RunResult, c Case) (RunResult, error) {
	payload := map[string]any{"model": result.Model, "stream": true, "input": c.Prompt, "reasoning": map[string]any{"effort": "max"}, "max_output_tokens": 512}
	b, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), r.client.Timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/responses", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		result.LatencyMS = time.Since(start).Milliseconds()
		return result, err
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	fillHeaders(&result, resp.Header)
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var out strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type, Delta string
			Response    struct {
				Status string
				Usage  struct{ InputTokens, OutputTokens int }
			}
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			continue
		}
		if event.Type == "response.output_text.delta" {
			if result.TTFTMS == 0 {
				result.TTFTMS = time.Since(start).Milliseconds()
			}
			out.WriteString(event.Delta)
		}
		if event.Type == "response.completed" {
			result.Status = event.Response.Status
			result.InputTokens, result.OutputTokens = event.Response.Usage.InputTokens, event.Response.Usage.OutputTokens
		}
	}
	result.LatencyMS = time.Since(start).Milliseconds()
	result.Output = out.String()
	if err := scanner.Err(); err != nil {
		return result, err
	}
	result.Correct = exact(result.Output, c.Expected)
	if !result.Correct {
		return result, fmt.Errorf("broken or incorrect stream: %s", truncate(result.Output, 160))
	}
	return result, nil
}

func (r *runner) runToolCase(result RunResult, model Model, c Case) (RunResult, error) {
	input := []any{map[string]any{"type": "message", "role": "user", "content": c.Prompt}}
	tools := toolsFor(c.Kind)
	reads := map[string]bool{}
	files := map[string]string{}
	if c.Kind == "workflow" {
		for _, name := range []string{"AGENTS.md", "calc.go", "calc_test.go"} {
			path := filepath.Join(r.root, "fixtures", name)
			if name != "AGENTS.md" {
				path = filepath.Join(r.root, "fixtures", "testdata", name)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return result, err
			}
			files[name] = string(b)
		}
	}
	writes := 0
	for turn := 0; turn < 7; turn++ {
		payload := map[string]any{"model": result.Model, "instructions": "Tool results are authoritative. Continue until the requested verified final answer is ready.", "input": input, "tools": tools, "parallel_tool_calls": false, "reasoning": map[string]any{"effort": "max"}, "max_output_tokens": 2048}
		body, headers, status, latency, err := r.post(payload)
		result.LatencyMS += latency
		fillHTTPNoLatency(&result, body, headers, status)
		if err != nil {
			return result, err
		}
		calls := callsFrom(body.Output)
		if len(calls) == 0 {
			result.Output = responseText(body.Output)
			result.Correct = exact(result.Output, c.Expected)
			if c.Kind == "workflow" {
				result.ReadBeforeWrite = reads["AGENTS.md"] && reads["calc.go"] && reads["calc_test.go"]
				result.Correct = workflowFinalAccurate(result.Output) && result.ReadBeforeWrite && writes > 0 && result.TestsPassed
			}
			if !result.Correct {
				return result, fmt.Errorf("tool workflow final answer or sequence was incorrect: %s", truncate(result.Output, 160))
			}
			return result, nil
		}
		for _, call := range calls {
			result.ToolCalls++
			input = append(input, callInput(call))
			output, valid, repair := executeVirtual(c.Kind, call, files, reads, &writes, &result)
			if !valid {
				result.InvalidToolArgs++
			}
			if repair {
				result.RepairAttempts++
			}
			input = append(input, callOutput(call, output))
		}
	}
	return result, fmt.Errorf("tool workflow exceeded turn limit")
}

func workflowFinalAccurate(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	if lower == "" || strings.Contains(lower, "did not run") || strings.Contains(lower, "not run") {
		return false
	}
	return strings.Contains(lower, "test") && (strings.Contains(lower, "pass") || strings.Contains(lower, "success"))
}

func (r *runner) post(payload map[string]any) (responseBody, http.Header, int, int64, error) {
	b, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), r.client.Timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/responses", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		return responseBody{}, nil, 0, time.Since(start).Milliseconds(), err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	latency := time.Since(start).Milliseconds()
	var body responseBody
	_ = json.Unmarshal(raw, &body)
	if resp.StatusCode >= 400 {
		message := body.Error.Message
		if message == "" {
			message = truncate(string(raw), 300)
		}
		return body, resp.Header, resp.StatusCode, latency, fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)
	}
	return body, resp.Header, resp.StatusCode, latency, nil
}

func toolsFor(kind string) []any {
	object := func(properties map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	if kind == "custom_tool" {
		return []any{map[string]any{"type": "custom", "name": "exec", "description": "Evaluate one raw arithmetic expression", "format": map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: SOURCE\nSOURCE: /[\\s\\S]+/"}}}
	}
	if kind == "function_tool" {
		return []any{map[string]any{"type": "function", "name": "lookup_number", "description": "Look up a fixed test number", "strict": true, "parameters": object(map[string]any{"key": map[string]any{"type": "string", "enum": []string{"alpha"}}}, []string{"key"})}}
	}
	return []any{
		map[string]any{"type": "function", "name": "read_file", "description": "Read a fixture repository file", "strict": true, "parameters": object(map[string]any{"path": map[string]any{"type": "string", "enum": []string{"AGENTS.md", "calc.go", "calc_test.go"}}}, []string{"path"})},
		map[string]any{"type": "function", "name": "write_file", "description": "Replace calc.go in the in-memory fixture only", "strict": true, "parameters": object(map[string]any{"path": map[string]any{"type": "string", "enum": []string{"calc.go"}}, "content": map[string]any{"type": "string"}}, []string{"path", "content"})},
		map[string]any{"type": "function", "name": "run_tests", "description": "Run deterministic tests in the in-memory fixture", "strict": true, "parameters": object(map[string]any{}, []string{})},
	}
}

func executeVirtual(kind string, call responseItem, files map[string]string, reads map[string]bool, writes *int, result *RunResult) (string, bool, bool) {
	if kind == "custom_tool" {
		valid := call.Type == "custom_tool_call" && call.Name == "exec" && strings.TrimSpace(call.Input) == "2+2"
		return "4", valid, !valid
	}
	var args map[string]any
	validJSON := json.Unmarshal([]byte(call.Arguments), &args) == nil
	if kind == "function_tool" {
		valid := validJSON && call.Name == "lookup_number" && len(args) == 1 && args["key"] == "alpha"
		return "42", valid, !valid
	}
	if !validJSON {
		return "ERROR: invalid JSON arguments", false, true
	}
	switch call.Name {
	case "read_file":
		path, ok := args["path"].(string)
		content, exists := files[path]
		if !ok || !exists || len(args) != 1 {
			return "ERROR: file not found or invalid path", false, true
		}
		reads[path] = true
		return content, true, false
	case "write_file":
		path, pok := args["path"].(string)
		content, cok := args["content"].(string)
		if !pok || !cok || path != "calc.go" || len(args) != 2 {
			return "ERROR: invalid write arguments", false, true
		}
		if !(reads["AGENTS.md"] && reads["calc.go"] && reads["calc_test.go"]) {
			return "ERROR: inspect AGENTS.md, calc.go, and calc_test.go before writing", false, true
		}
		files[path] = content
		*writes++
		return "write succeeded", true, false
	case "run_tests":
		result.TestsRun = true
		if len(args) != 0 {
			return "ERROR: run_tests takes no arguments", false, true
		}
		if strings.Contains(files["calc.go"], "return a + b") {
			result.TestsPassed = true
			return "PASS", true, false
		}
		result.TestsPassed = false
		return "FAIL: Add(2, 3) did not equal 5", true, false
	default:
		return "ERROR: invented tool", false, true
	}
}

func callsFrom(items []responseItem) []responseItem {
	var calls []responseItem
	for _, item := range items {
		if item.Type == "function_call" || item.Type == "custom_tool_call" {
			calls = append(calls, item)
		}
	}
	return calls
}

func callInput(call responseItem) map[string]any {
	if call.Type == "custom_tool_call" {
		return map[string]any{"type": call.Type, "call_id": call.CallID, "name": call.Name, "input": call.Input}
	}
	return map[string]any{"type": call.Type, "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments}
}

func callOutput(call responseItem, output string) map[string]any {
	t := "function_call_output"
	if call.Type == "custom_tool_call" {
		t = "custom_tool_call_output"
	}
	return map[string]any{"type": t, "call_id": call.CallID, "output": output}
}

func responseText(items []responseItem) string {
	var out strings.Builder
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		var parts []struct{ Type, Text string }
		if json.Unmarshal(item.Content, &parts) == nil {
			for _, part := range parts {
				if part.Type == "output_text" || part.Type == "text" {
					out.WriteString(part.Text)
				}
			}
		}
	}
	return out.String()
}

func fillHTTP(result *RunResult, body responseBody, headers http.Header, status int, latency int64) {
	result.LatencyMS = latency
	fillHTTPNoLatency(result, body, headers, status)
}

func fillHTTPNoLatency(result *RunResult, body responseBody, headers http.Header, status int) {
	result.HTTPStatus, result.Status = status, body.Status
	result.InputTokens += body.Usage.InputTokens
	result.OutputTokens += body.Usage.OutputTokens
	fillHeaders(result, headers)
}

func fillHeaders(result *RunResult, h http.Header) {
	if h == nil {
		return
	}
	result.RequestedModel = h.Get("X-ACC-Requested-Model")
	result.RequestedEffort = h.Get("X-ACC-Requested-Effort")
	result.ActualProvider = h.Get("X-ACC-Backend-Provider")
	result.ActualModel = h.Get("X-ACC-Backend-Model")
	result.ActualEffort = h.Get("X-ACC-Backend-Effort")
	result.Fallback = strings.EqualFold(h.Get("X-ACC-Fallback"), "true")
}

func classify(status int, message string) string {
	lower := strings.ToLower(message)
	switch {
	case status == 429:
		return "provider_rate_limit"
	case status >= 500 || strings.Contains(lower, "timeout") || strings.Contains(lower, "connection"):
		return "provider_or_transport"
	case strings.Contains(lower, "tool") || strings.Contains(lower, "arguments") || strings.Contains(lower, "schema"):
		return "tool_formatting"
	case status >= 400:
		return "capability_or_request"
	default:
		return "incorrect_final_answer"
	}
}

func exact(output, expected string) bool { return strings.TrimSpace(output) == expected }

func longPrompt(tokens int) string {
	var b strings.Builder
	b.WriteString("Keep these exact constraints: ALPHA-7319 and NO_DESTRUCTIVE_ACTION. ")
	filler := "Repository observation: ordinary line with no instruction. "
	for b.Len() < tokens*4-200 {
		b.WriteString(filler)
	}
	b.WriteString(" Final marker OMEGA-2846. Reply exactly ALPHA-7319|OMEGA-2846|NO_DESTRUCTIVE_ACTION.")
	return b.String()
}

func applicable(model Model, c Case) bool {
	switch c.Kind {
	case "function_tool", "workflow":
		return model.Tools
	case "custom_tool":
		return model.CustomTools
	case "image":
		return model.Images
	default:
		return true
	}
}

func filterCases(cases []Case, profile string) []Case {
	var out []Case
	for _, c := range cases {
		switch profile {
		case "probe":
			if c.ID == "text-exact" {
				c.Runs = 1
				out = append(out, c)
			}
		case "core":
			if c.Kind != "long_context" && c.Kind != "image" {
				out = append(out, c)
			}
		case "full":
			out = append(out, c)
		default:
			panic("unknown profile " + profile)
		}
	}
	return out
}

func filterModels(models []Model, filter string) []Model {
	if strings.TrimSpace(filter) == "" {
		return models
	}
	want := map[string]bool{}
	for _, id := range strings.Split(filter, ",") {
		want[strings.TrimSpace(id)] = true
	}
	var out []Model
	for _, model := range models {
		if want[model.ID] {
			out = append(out, model)
		}
	}
	return out
}

func filterCaseIDs(cases []Case, filter string) []Case {
	if strings.TrimSpace(filter) == "" {
		return cases
	}
	want := map[string]bool{}
	for _, id := range strings.Split(filter, ",") {
		want[strings.TrimSpace(id)] = true
	}
	var out []Case
	for _, c := range cases {
		if want[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

func findRoot() (string, error) {
	for _, root := range []string{"benchmarks/model-routing", "."} {
		if _, err := os.Stat(filepath.Join(root, "cases", "models.json")); err == nil {
			return root, nil
		}
	}
	return "", fmt.Errorf("run from the ACC repository root or benchmarks/model-routing")
}

func loadJSON[T any](path string) T {
	var value T
	b, err := os.ReadFile(path)
	must(err)
	must(json.Unmarshal(b, &value))
	return value
}

func writeRaw(root string, result RunResult) {
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(result.Timestamp)
	name := fmt.Sprintf("%s_%s_%02d_%s.json", result.Model, result.CaseID, result.Run, stamp)
	b, _ := json.MarshalIndent(result, "", "  ")
	must(os.WriteFile(filepath.Join(root, "raw-results", name), append(b, '\n'), 0644))
}

type summary struct {
	Model                                                                                     string `json:"model"`
	Runs, Successful, Correct                                                                 int
	ProviderSuccessRate, CorrectRate, ToolSuccessRate, ToolSchemaErrorRate, RepairSuccessRate float64
	AverageLatencyMS, AverageTTFTMS                                                           int64
	InputTokens, OutputTokens                                                                 int
	Categories                                                                                map[string]float64
}

func writeSummary(root, profile string, results []RunResult) {
	byModel := map[string][]RunResult{}
	for _, r := range results {
		byModel[r.Model] = append(byModel[r.Model], r)
	}
	var summaries []summary
	for model, runs := range byModel {
		s := summary{Model: model, Runs: len(runs), Categories: map[string]float64{}}
		var latency, ttft int64
		var ttftN, toolN, toolOK, invalid, repairs, repaired int
		catN, catOK := map[string]int{}, map[string]int{}
		for _, r := range runs {
			if r.HTTPStatus >= 200 && r.HTTPStatus < 300 {
				s.Successful++
			}
			if r.Correct {
				s.Correct++
				catOK[r.Category]++
			}
			catN[r.Category]++
			latency += r.LatencyMS
			if r.TTFTMS > 0 {
				ttft += r.TTFTMS
				ttftN++
			}
			s.InputTokens += r.InputTokens
			s.OutputTokens += r.OutputTokens
			if r.Category == "tools" || r.Category == "custom_tools" || r.Category == "coding" {
				toolN++
				if r.Correct {
					toolOK++
				}
				invalid += r.InvalidToolArgs
				repairs += r.RepairAttempts
				if r.RepairAttempts > 0 && r.Correct {
					repaired += r.RepairAttempts
				}
			}
		}
		s.ProviderSuccessRate = pct(s.Successful, s.Runs)
		s.CorrectRate = pct(s.Correct, s.Runs)
		s.ToolSuccessRate = pct(toolOK, toolN)
		s.ToolSchemaErrorRate = pct(invalid, max(1, toolN))
		s.RepairSuccessRate = pct(repaired, repairs)
		s.AverageLatencyMS = latency / int64(max(1, len(runs)))
		if ttftN > 0 {
			s.AverageTTFTMS = ttft / int64(ttftN)
		}
		for cat, n := range catN {
			s.Categories[cat] = pct(catOK[cat], n)
		}
		summaries = append(summaries, s)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Model < summaries[j].Model })
	doc := map[string]any{"generated_at": time.Now().Format(time.RFC3339), "profile": profile, "reasoning": map[string]string{"mode": "maximum"}, "models": summaries, "runs": results}
	b, _ := json.MarshalIndent(doc, "", "  ")
	must(os.WriteFile(filepath.Join(root, "results.json"), append(b, '\n'), 0644))
	var md strings.Builder
	md.WriteString("# ACC model-routing report\n\n")
	fmt.Fprintf(&md, "Generated: %s  \nProfile: `%s`  \nReasoning: `maximum`\n\n", time.Now().Format(time.RFC3339), profile)
	md.WriteString("| Model | Runs | Provider success | Correct | Tool success | Avg latency | Avg TTFT |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, s := range summaries {
		fmt.Fprintf(&md, "| %s | %d | %.1f%% | %.1f%% | %.1f%% | %d ms | %d ms |\n", s.Model, s.Runs, s.ProviderSuccessRate, s.CorrectRate, s.ToolSuccessRate, s.AverageLatencyMS, s.AverageTTFTMS)
	}
	must(os.WriteFile(filepath.Join(root, "report.md"), []byte(md.String()), 0644))
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100 / float64(d)
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
