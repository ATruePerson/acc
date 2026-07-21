package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultWebFetchTimeout = 20 * time.Second
	defaultWebFetchChars   = 50_000
	maxWebFetchChars       = 200_000
	maxWebFetchBytes       = 5 << 20
	maxWebRedirects        = 5
)

var webUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

type webFetchOptions struct {
	AllowPrivate bool
	Client       *http.Client
	Timeout      time.Duration
	MaxChars     int
	MaxBytes     int64
}

type webPage struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	ContentType string `json:"contentType"`
	Truncated   bool   `json:"truncated"`
}

type webSearchItem struct {
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	Engagement map[string]any `json:"engagement"`
	When       string         `json:"when,omitempty"`
	By         string         `json:"by,omitempty"`
	Snippet    string         `json:"snippet,omitempty"`
}

type webSourceResult struct {
	Source string          `json:"source"`
	Items  []webSearchItem `json:"items,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var (
	titlePattern   = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	articlePattern = regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article>`)
	mainPattern    = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`)
	removePattern  = regexp.MustCompile(`(?is)<(?:script|style|noscript|template|svg|nav|header|footer|aside|form)\b[^>]*>.*?</(?:script|style|noscript|template|svg|nav|header|footer|aside|form)\s*>`)
	tagPattern     = regexp.MustCompile(`(?is)<[^>]+>`)
	breakPattern   = regexp.MustCompile(`(?is)<br\s*/?>|</(p|div|li|tr|h[1-6]|blockquote|section|article|pre)>`)
	spacePattern   = regexp.MustCompile(`[ \t\f\v]+`)
	blankPattern   = regexp.MustCompile(`\n{3,}`)
)

func fetchWebPage(ctx context.Context, target string, options webFetchOptions) (webPage, error) {
	if options.Timeout <= 0 {
		options.Timeout = defaultWebFetchTimeout
	}
	if options.MaxChars <= 0 {
		options.MaxChars = defaultWebFetchChars
	}
	if options.MaxChars > maxWebFetchChars {
		options.MaxChars = maxWebFetchChars
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = maxWebFetchBytes
	}
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	current := target
	for redirects := 0; redirects <= maxWebRedirects; redirects++ {
		parsed, err := validateFetchURL(ctx, current, options.AllowPrivate)
		if err != nil {
			return webPage{}, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return webPage{}, err
		}
		request.Header.Set("User-Agent", webUserAgent)
		request.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.2")
		request.Header.Set("Accept-Language", "en-US,en;q=0.9")
		response, err := clientCopy.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return webPage{}, fmt.Errorf("fetch timed out after %s", options.Timeout)
			}
			return webPage{}, fmt.Errorf("fetch failed: %w", err)
		}

		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			if location == "" {
				return webPage{}, fmt.Errorf("HTTP %d redirect has no Location header", response.StatusCode)
			}
			if redirects == maxWebRedirects {
				return webPage{}, fmt.Errorf("too many redirects")
			}
			next, err := parsed.Parse(location)
			if err != nil {
				return webPage{}, fmt.Errorf("bad redirect: %w", err)
			}
			current = next.String()
			continue
		}

		body, truncatedBytes, readErr := readLimited(response.Body, options.MaxBytes)
		_ = response.Body.Close()
		if readErr != nil {
			return webPage{}, fmt.Errorf("read response: %w", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			message := strings.TrimSpace(string(body))
			if len(message) > 200 {
				message = message[:200]
			}
			if message == "" {
				return webPage{}, fmt.Errorf("HTTP %d", response.StatusCode)
			}
			return webPage{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, message)
		}

		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if !isReadableContentType(contentType) {
			return webPage{}, fmt.Errorf("unsupported content type %q", contentType)
		}
		raw := string(body)
		page := webPage{URL: response.Request.URL.String(), Status: response.StatusCode, ContentType: contentType}
		if strings.Contains(contentType, "html") || strings.Contains(strings.ToLower(raw), "<html") {
			page.Title, page.Text = readableHTML(raw)
		} else {
			page.Text = strings.TrimSpace(raw)
		}
		page.Truncated = truncatedBytes
		if len(page.Text) > options.MaxChars {
			page.Text = page.Text[:options.MaxChars]
			page.Truncated = true
		}
		return page, nil
	}
	return webPage{}, fmt.Errorf("too many redirects")
}

func validateFetchURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme must be http or https")
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("URL must have a host and no embedded credentials")
	}
	if allowPrivate {
		return parsed, nil
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, fmt.Errorf("private or local hosts are blocked")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if isPrivateAddress(address) {
			return nil, fmt.Errorf("private or local hosts are blocked")
		}
		return parsed, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	for _, address := range addresses {
		parsedAddress, ok := netip.AddrFromSlice(address.IP)
		if ok && isPrivateAddress(parsedAddress.Unmap()) {
			return nil, fmt.Errorf("private or local hosts are blocked")
		}
	}
	return parsed, nil
}

func isPrivateAddress(address netip.Addr) bool {
	return !address.IsValid() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast()
}

func readLimited(reader io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func isReadableContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	for _, allowed := range []string{"text/", "application/json", "application/xml", "application/xhtml+xml", "application/rss+xml", "application/atom+xml"} {
		if strings.Contains(contentType, allowed) {
			return true
		}
	}
	return false
}

func readableHTML(source string) (string, string) {
	title := ""
	if match := titlePattern.FindStringSubmatch(source); len(match) == 2 {
		title = cleanInlineText(match[1])
	}
	content := source
	if match := articlePattern.FindStringSubmatch(source); len(match) == 2 {
		content = match[1]
	} else if match := mainPattern.FindStringSubmatch(source); len(match) == 2 {
		content = match[1]
	}
	content = removePattern.ReplaceAllString(content, " ")
	content = breakPattern.ReplaceAllString(content, "\n")
	content = tagPattern.ReplaceAllString(content, " ")
	content = html.UnescapeString(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r", ""), "\n")
	cleaned := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(spacePattern.ReplaceAllString(line, " "))
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	text := strings.Join(cleaned, "\n")
	text = blankPattern.ReplaceAllString(text, "\n\n")
	return title, strings.TrimSpace(text)
}

func cleanInlineText(source string) string {
	return strings.TrimSpace(spacePattern.ReplaceAllString(html.UnescapeString(tagPattern.ReplaceAllString(source, " ")), " "))
}

func newWebsearchMCPServer() *mcpServer {
	readOnlyOpen := map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true}
	server := &mcpServer{
		Name: "acc-websearch", Version: "3.0.0",
		Tools: []mcpTool{
			{
				Name: "web_search", Description: "Keyless engagement-first search across Hacker News, GitHub, Polymarket, Reddit, and the general web. Individual source failures are returned without losing successful sources.",
				Annotations: readOnlyOpen,
				InputSchema: objectSchema(map[string]any{
					"query":   map[string]any{"type": "string", "description": "Search query"},
					"count":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Results per source, default 6"},
					"sources": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"hackernews", "github", "polymarket", "reddit", "web"}}},
				}, []string{"query"}),
			},
			{
				Name: "web_fetch", Description: "Fetch one public HTTP(S) URL and return readable text. Rejects error pages, private-network targets, oversized responses, unsupported binary content, redirect loops, and timeouts.",
				Annotations: readOnlyOpen,
				InputSchema: objectSchema(map[string]any{
					"url":      map[string]any{"type": "string", "description": "Public HTTP(S) URL"},
					"maxChars": map[string]any{"type": "integer", "minimum": 1, "maximum": maxWebFetchChars, "description": "Maximum readable characters, default 50000"},
				}, []string{"url"}),
			},
		},
		Handlers: map[string]mcpToolHandler{},
	}
	server.Handlers["web_fetch"] = func(ctx context.Context, args map[string]any) (any, error) {
		target, err := requiredString(args, "url")
		if err != nil {
			return nil, err
		}
		return fetchWebPage(ctx, target, webFetchOptions{MaxChars: intArg(args, "maxChars", defaultWebFetchChars)})
	}
	server.Handlers["web_search"] = func(ctx context.Context, args map[string]any) (any, error) {
		query, err := requiredString(args, "query")
		if err != nil {
			return nil, err
		}
		return runWebSearch(ctx, query, intArg(args, "count", 6), stringSliceArg(args, "sources")), nil
	}
	return server
}

func runWebSearch(ctx context.Context, query string, count int, requested []string) map[string]any {
	if count < 1 {
		count = 1
	}
	if count > 10 {
		count = 10
	}
	allSources := map[string]func(context.Context, string, int) webSourceResult{
		"hackernews": searchHackerNews,
		"github":     searchGitHub,
		"polymarket": searchPolymarket,
		"reddit":     searchReddit,
		"web":        searchDuckDuckGo,
	}
	if len(requested) == 0 {
		requested = []string{"hackernews", "github", "polymarket", "reddit", "web"}
	}
	wanted := make([]string, 0, len(requested))
	for _, name := range requested {
		if _, ok := allSources[name]; ok {
			wanted = append(wanted, name)
		}
	}
	results := make(map[string]webSourceResult, len(wanted))
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, name := range wanted {
		name := name
		wait.Add(1)
		go func() {
			defer wait.Done()
			sourceCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			result := allSources[name](sourceCtx, query, count)
			mutex.Lock()
			results[name] = result
			mutex.Unlock()
		}()
	}
	wait.Wait()
	return map[string]any{"query": query, "sources": wanted, "results": results}
}

func searchHackerNews(ctx context.Context, query string, count int) webSourceResult {
	var payload struct {
		Hits []struct {
			Title       string `json:"title"`
			StoryTitle  string `json:"story_title"`
			URL         string `json:"url"`
			ObjectID    string `json:"objectID"`
			CreatedAt   string `json:"created_at"`
			Author      string `json:"author"`
			Points      int    `json:"points"`
			NumComments int    `json:"num_comments"`
		} `json:"hits"`
	}
	endpoint := "https://hn.algolia.com/api/v1/search?tags=story&hitsPerPage=" + strconv.Itoa(count*2) + "&query=" + url.QueryEscape(query)
	if err := getWebJSON(ctx, endpoint, &payload); err != nil {
		return webSourceResult{Source: "hackernews", Error: err.Error()}
	}
	sort.Slice(payload.Hits, func(i, j int) bool { return payload.Hits[i].Points > payload.Hits[j].Points })
	items := make([]webSearchItem, 0, count)
	for _, hit := range payload.Hits {
		if len(items) == count {
			break
		}
		title := hit.Title
		if title == "" {
			title = hit.StoryTitle
		}
		link := hit.URL
		if link == "" {
			link = "https://news.ycombinator.com/item?id=" + hit.ObjectID
		}
		items = append(items, webSearchItem{Title: title, URL: link, Engagement: map[string]any{"points": hit.Points, "comments": hit.NumComments}, When: hit.CreatedAt, By: hit.Author})
	}
	return webSourceResult{Source: "hackernews", Items: items}
}

func searchGitHub(ctx context.Context, query string, count int) webSourceResult {
	var payload struct {
		Items []struct {
			FullName        string `json:"full_name"`
			HTMLURL         string `json:"html_url"`
			Description     string `json:"description"`
			UpdatedAt       string `json:"updated_at"`
			StargazersCount int    `json:"stargazers_count"`
			ForksCount      int    `json:"forks_count"`
			OpenIssuesCount int    `json:"open_issues_count"`
			Owner           struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"items"`
	}
	endpoint := "https://api.github.com/search/repositories?sort=stars&order=desc&per_page=" + strconv.Itoa(count) + "&q=" + url.QueryEscape(query)
	if err := getWebJSON(ctx, endpoint, &payload); err != nil {
		return webSourceResult{Source: "github", Error: err.Error()}
	}
	items := make([]webSearchItem, 0, len(payload.Items))
	for _, repository := range payload.Items {
		items = append(items, webSearchItem{Title: repository.FullName, URL: repository.HTMLURL, Engagement: map[string]any{"stars": repository.StargazersCount, "forks": repository.ForksCount, "issues": repository.OpenIssuesCount}, When: repository.UpdatedAt, By: repository.Owner.Login, Snippet: repository.Description})
	}
	return webSourceResult{Source: "github", Items: items}
}

func searchPolymarket(ctx context.Context, query string, count int) webSourceResult {
	var payload []struct {
		Title       string `json:"title"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		EndDate     string `json:"endDate"`
		StartDate   string `json:"startDate"`
		Volume      any    `json:"volume"`
		Liquidity   any    `json:"liquidity"`
	}
	endpoint := "https://gamma-api.polymarket.com/events?search=" + url.QueryEscape(query) + "&limit=" + strconv.Itoa(count*2) + "&closed=false&order=volume&ascending=false"
	if err := getWebJSON(ctx, endpoint, &payload); err != nil {
		return webSourceResult{Source: "polymarket", Error: err.Error()}
	}
	items := make([]webSearchItem, 0, count)
	for _, event := range payload {
		if len(items) == count {
			break
		}
		link := "https://polymarket.com"
		if event.Slug != "" {
			link += "/event/" + event.Slug
		}
		when := event.EndDate
		if when == "" {
			when = event.StartDate
		}
		items = append(items, webSearchItem{Title: event.Title, URL: link, Engagement: map[string]any{"volume": event.Volume, "liquidity": event.Liquidity}, When: when, Snippet: truncateString(event.Description, 200)})
	}
	return webSourceResult{Source: "polymarket", Items: items}
}

func searchReddit(ctx context.Context, query string, count int) webSourceResult {
	var payload struct {
		Data struct {
			Children []struct {
				Data struct {
					Title       string  `json:"title"`
					Permalink   string  `json:"permalink"`
					Author      string  `json:"author"`
					Subreddit   string  `json:"subreddit"`
					Selftext    string  `json:"selftext"`
					Ups         int     `json:"ups"`
					NumComments int     `json:"num_comments"`
					UpvoteRatio float64 `json:"upvote_ratio"`
					CreatedUTC  float64 `json:"created_utc"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	endpoint := "https://www.reddit.com/search.json?sort=top&t=all&limit=" + strconv.Itoa(count) + "&q=" + url.QueryEscape(query)
	if err := getWebJSON(ctx, endpoint, &payload); err != nil {
		return webSourceResult{Source: "reddit", Error: err.Error() + " (Reddit often blocks keyless requests)"}
	}
	items := make([]webSearchItem, 0, len(payload.Data.Children))
	for _, child := range payload.Data.Children {
		post := child.Data
		items = append(items, webSearchItem{Title: post.Title, URL: "https://www.reddit.com" + post.Permalink, Engagement: map[string]any{"upvotes": post.Ups, "comments": post.NumComments, "ratio": post.UpvoteRatio}, When: time.Unix(int64(post.CreatedUTC), 0).UTC().Format(time.RFC3339), By: "u/" + post.Author, Snippet: "r/" + post.Subreddit + " - " + truncateString(post.Selftext, 160)})
	}
	return webSourceResult{Source: "reddit", Items: items}
}

var duckResultPattern = regexp.MustCompile(`(?is)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)

func searchDuckDuckGo(ctx context.Context, query string, count int) webSourceResult {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(query), nil)
	request.Header.Set("User-Agent", webUserAgent)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return webSourceResult{Source: "web", Error: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return webSourceResult{Source: "web", Error: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	body, _, err := readLimited(response.Body, 2<<20)
	if err != nil {
		return webSourceResult{Source: "web", Error: err.Error()}
	}
	matches := duckResultPattern.FindAllStringSubmatch(string(body), count)
	items := make([]webSearchItem, 0, len(matches))
	for _, match := range matches {
		link := html.UnescapeString(match[1])
		if parsed, err := url.Parse(link); err == nil {
			if redirected := parsed.Query().Get("uddg"); redirected != "" {
				link = redirected
			}
		}
		if strings.HasPrefix(link, "//") {
			link = "https:" + link
		}
		items = append(items, webSearchItem{Title: cleanInlineText(match[2]), URL: link, Engagement: map[string]any{}})
	}
	return webSourceResult{Source: "web", Items: items}
}

func getWebJSON(ctx context.Context, endpoint string, target any) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	request.Header.Set("User-Agent", webUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 5<<20)).Decode(target)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func requiredString(args map[string]any, name string) (string, error) {
	value, _ := args[name].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func optionalString(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func intArg(args map[string]any, name string, fallback int) int {
	switch value := args[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func boolArg(args map[string]any, name string, fallback bool) bool {
	value, ok := args[name].(bool)
	if !ok {
		return fallback
	}
	return value
}

func stringSliceArg(args map[string]any, name string) []string {
	values, ok := args[name].([]any)
	if !ok {
		if direct, ok := args[name].([]string); ok {
			return direct
		}
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
