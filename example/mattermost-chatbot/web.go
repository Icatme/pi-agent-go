package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	core "github.com/Icatme/pi-agent-go"
)

const (
	defaultDuckDuckGoSearchURL = "https://html.duckduckgo.com/html"
	defaultUserAgent           = "Mozilla/5.0 (compatible; pi-agent-go mattermost chatbot/1.0)"
	maxResponseBodyBytes       = 2 << 20
	defaultRequestTimeout      = 15 * time.Second
	maxRedirects               = 5
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type pageSnapshot struct {
	URL         string
	Title       string
	Description string
	ContentType string
	Text        string
	Links       []string
	Truncated   bool
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func newRestrictedHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = restrictedDialContext(transport.DialContext)

	return &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return validatePublicWebURL(req.URL)
		},
	}
}

func restrictedDialContext(base func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	if base == nil {
		base = (&net.Dialer{Timeout: defaultRequestTimeout}).DialContext
	}

	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if err := validatePublicHost(host); err != nil {
			return nil, err
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if err := validatePublicIP(ip.IP); err != nil {
				return nil, err
			}
		}

		return base(ctx, network, address)
	}
}

func validatePublicWebURL(parsed *url.URL) error {
	if parsed == nil {
		return errors.New("missing URL")
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("URL host is required")
	}
	return validatePublicHost(parsed.Hostname())
}

func validatePublicHost(host string) error {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return errors.New("URL host is required")
	}

	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") {
		return fmt.Errorf("local or private addresses are not allowed")
	}

	if addr, err := netip.ParseAddr(lowerHost); err == nil {
		return validatePublicAddr(addr)
	}
	return nil
}

func validatePublicIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("invalid IP address")
	}
	return validatePublicAddr(addr.Unmap())
}

func validatePublicAddr(addr netip.Addr) error {
	if addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalMulticast() || addr.IsLinkLocalUnicast() || addr.IsPrivate() || addr.IsUnspecified() {
		return fmt.Errorf("local or private addresses are not allowed")
	}
	return nil
}

func (r toolRuntime) executeWebSearch(ctx context.Context, args webSearchArgs) (core.ToolResult, error) {
	searchURL, err := url.Parse(r.searchBaseURL)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_search: invalid search endpoint: %w", err)
	}

	query := searchURL.Query()
	query.Set("q", args.Query)
	searchURL.RawQuery = query.Encode()

	body, finalURL, _, err := fetchBody(ctx, r.client, searchURL.String())
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_search: %w", err)
	}

	results, err := parseDuckDuckGoHTML(body, args.Limit)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_search: %w", err)
	}

	var builder strings.Builder
	if len(results) == 0 {
		builder.WriteString("No search results found.")
	} else {
		for index, result := range results {
			fmt.Fprintf(&builder, "%d. %s\n%s\n", index+1, result.Title, result.URL)
			if result.Snippet != "" {
				builder.WriteString(result.Snippet)
				builder.WriteString("\n")
			}
			if index < len(results)-1 {
				builder.WriteString("\n")
			}
		}
	}

	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(builder.String())},
		Details: map[string]any{
			"query":      args.Query,
			"final_url":  finalURL,
			"result_cnt": len(results),
			"results":    results,
		},
	}, nil
}

func (r toolRuntime) executeWebFetch(ctx context.Context, args webFetchArgs) (core.ToolResult, error) {
	page, err := fetchPageSnapshot(ctx, r.client, args.URL, args.MaxChars)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_fetch: %w", err)
	}

	text := page.Text
	if page.Title != "" {
		text = page.Title + "\n\n" + text
	}

	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(strings.TrimSpace(text))},
		Details: map[string]any{
			"url":          page.URL,
			"title":        page.Title,
			"description":  page.Description,
			"content_type": page.ContentType,
			"truncated":    page.Truncated,
		},
	}, nil
}

func (r toolRuntime) executeWebPageMeta(ctx context.Context, args webPageMetaArgs) (core.ToolResult, error) {
	page, err := fetchPageSnapshot(ctx, r.client, args.URL, defaultFetchChars)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_page_meta: %w", err)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Title: %s\n", fallbackString(page.Title, "(none)"))
	fmt.Fprintf(&builder, "URL: %s\n", page.URL)
	fmt.Fprintf(&builder, "Content-Type: %s", fallbackString(page.ContentType, "(unknown)"))
	if page.Description != "" {
		fmt.Fprintf(&builder, "\nDescription: %s", page.Description)
	}

	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(builder.String())},
		Details: map[string]any{
			"url":          page.URL,
			"title":        page.Title,
			"description":  page.Description,
			"content_type": page.ContentType,
		},
	}, nil
}

func (r toolRuntime) executeWebExtractLinks(ctx context.Context, args webExtractLinksArgs) (core.ToolResult, error) {
	page, err := fetchPageSnapshot(ctx, r.client, args.URL, defaultFetchChars)
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("web_extract_links: %w", err)
	}

	links := page.Links
	if len(links) > args.Limit {
		links = slices.Clone(links[:args.Limit])
	}

	var builder strings.Builder
	if len(links) == 0 {
		builder.WriteString("No links found.")
	} else {
		for _, link := range links {
			builder.WriteString(link)
			builder.WriteString("\n")
		}
	}

	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(strings.TrimSpace(builder.String()))},
		Details: map[string]any{
			"url":   page.URL,
			"links": links,
			"count": len(links),
		},
	}, nil
}

func fetchPageSnapshot(ctx context.Context, client httpDoer, rawURL string, maxChars int) (pageSnapshot, error) {
	body, finalURL, contentType, err := fetchBody(ctx, client, rawURL)
	if err != nil {
		return pageSnapshot{}, err
	}

	snapshot := pageSnapshot{
		URL:         finalURL,
		ContentType: contentType,
	}

	switch {
	case isHTMLContentType(contentType):
		parsedURL, err := url.Parse(finalURL)
		if err != nil {
			return pageSnapshot{}, err
		}
		title, description, text, links, err := parseHTMLDocument(body, parsedURL)
		if err != nil {
			return pageSnapshot{}, err
		}
		snapshot.Title = title
		snapshot.Description = description
		snapshot.Text, snapshot.Truncated = truncateText(text, maxChars)
		snapshot.Links = links
	case isPlainTextContentType(contentType):
		snapshot.Text, snapshot.Truncated = truncateText(normalizeWhitespace(string(body)), maxChars)
	default:
		return pageSnapshot{}, fmt.Errorf("unsupported content type %q", contentType)
	}

	return snapshot, nil
}

func fetchBody(ctx context.Context, client httpDoer, rawURL string) ([]byte, string, string, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid URL: %w", err)
	}
	if err := validatePublicWebURL(parsedURL); err != nil {
		return nil, "", "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	request.Header.Set("User-Agent", defaultUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	response, err := client.Do(request)
	if err != nil {
		return nil, "", "", err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}

	reader := io.LimitReader(response.Body, maxResponseBodyBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", "", err
	}
	if len(body) > maxResponseBodyBytes {
		return nil, "", "", fmt.Errorf("response body exceeds %d bytes", maxResponseBodyBytes)
	}

	contentType := response.Header.Get("Content-Type")
	if response.Request != nil && response.Request.URL != nil {
		return body, response.Request.URL.String(), contentType, nil
	}
	return body, parsedURL.String(), contentType, nil
}

func parseHTMLDocument(body []byte, baseURL *url.URL) (string, string, string, []string, error) {
	document, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", "", "", nil, err
	}

	var (
		title       string
		description string
		textParts   []string
		links       []string
		seenLinks   = map[string]struct{}{}
	)

	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, skipText bool) {
		if node == nil {
			return
		}

		currentSkip := skipText || shouldSkipNode(node)
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "title":
				if title == "" {
					title = normalizeWhitespace(extractNodeText(node))
				}
			case "meta":
				name := strings.ToLower(attrValue(node, "name"))
				if description == "" && name == "description" {
					description = normalizeWhitespace(attrValue(node, "content"))
				}
			case "a":
				href := normalizePageLink(baseURL, attrValue(node, "href"))
				if href != "" {
					if _, ok := seenLinks[href]; !ok {
						seenLinks[href] = struct{}{}
						links = append(links, href)
					}
				}
			}
		}

		if node.Type == xhtml.TextNode && !currentSkip {
			text := normalizeWhitespace(node.Data)
			if text != "" {
				textParts = append(textParts, text)
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, currentSkip)
		}
	}

	walk(document, false)

	return title, description, normalizeWhitespace(strings.Join(textParts, " ")), links, nil
}

func parseDuckDuckGoHTML(body []byte, limit int) ([]searchResult, error) {
	document, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	var (
		results       []searchResult
		currentResult = -1
	)

	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}

		if node.Type == xhtml.ElementNode {
			className := attrValue(node, "class")
			switch {
			case node.Data == "a" && hasClass(className, "result__a"):
				if len(results) < limit {
					title := normalizeWhitespace(extractNodeText(node))
					href := normalizeSearchResultURL(attrValue(node, "href"))
					if title != "" && href != "" {
						results = append(results, searchResult{Title: title, URL: href})
						currentResult = len(results) - 1
					}
				}
			case hasClass(className, "result__snippet"):
				if currentResult >= 0 && currentResult < len(results) && results[currentResult].Snippet == "" {
					results[currentResult].Snippet = normalizeWhitespace(extractNodeText(node))
				}
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(document)
	return results, nil
}

func normalizeSearchResultURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if strings.Contains(parsed.Host, "duckduckgo.com") && parsed.Path == "/l/" {
		if uddg := parsed.Query().Get("uddg"); uddg != "" {
			decoded, err := url.QueryUnescape(uddg)
			if err == nil {
				return decoded
			}
		}
	}
	return parsed.String()
}

func normalizePageLink(baseURL *url.URL, raw string) string {
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	resolved := baseURL.ResolveReference(parsed)
	if err := validatePublicWebURL(resolved); err != nil {
		return ""
	}
	resolved.Fragment = ""
	return resolved.String()
}

func isHTMLContentType(contentType string) bool {
	lower := strings.ToLower(contentType)
	return strings.Contains(lower, "text/html") || strings.Contains(lower, "application/xhtml+xml")
}

func isPlainTextContentType(contentType string) bool {
	lower := strings.ToLower(contentType)
	return strings.Contains(lower, "text/plain")
}

func shouldSkipNode(node *xhtml.Node) bool {
	return node.Type == xhtml.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript" || node.Data == "svg")
}

func extractNodeText(node *xhtml.Node) string {
	var parts []string

	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil || shouldSkipNode(current) {
			return
		}
		if current.Type == xhtml.TextNode {
			text := normalizeWhitespace(current.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(node)
	return normalizeWhitespace(strings.Join(parts, " "))
}

func attrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return html.UnescapeString(attr.Val)
		}
	}
	return ""
}

func hasClass(className, wanted string) bool {
	for _, token := range strings.Fields(className) {
		if token == wanted {
			return true
		}
	}
	return false
}

func normalizeWhitespace(text string) string {
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(html.UnescapeString(text)), " ")
}

func truncateText(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = defaultFetchChars
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, false
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "...", true
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
