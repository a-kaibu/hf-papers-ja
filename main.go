package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	feedURL          = "https://azuresilent.github.io/hf-paper-rss/feed.xml"
	defaultLLMBase   = "http://127.0.0.1:8080"
	outputPath       = "public/data/papers.json"
	defaultSchema    = "schemas/paper_explanation.schema.json"
	requestTimeout   = 5 * time.Minute
	feedHTTPTimeout  = 30 * time.Second
	maxAbstractChars = 6500
)

var (
	tagRegexp        = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRegexp      = regexp.MustCompile(`\s+`)
	paragraphRegexp  = regexp.MustCompile(`(?is)<p\b[^>]*>(.*?)</p>`)
	summaryBlockRE   = regexp.MustCompile(`(?is)<h3>\s*AI summary\s*</h3>(.*?)(?:<h3>|</div>)`)
	abstractBlockRE  = regexp.MustCompile(`(?is)<h3>\s*Abstract\s*</h3>(.*?)(?:<h3>|</div>)`)
	arxivAbsRegexp   = regexp.MustCompile(`https://arxiv\.org/abs/[0-9.]+`)
	arxivPDFRegexp   = regexp.MustCompile(`https://arxiv\.org/pdf/[0-9.]+(?:\.pdf)?`)
	jsonObjectRegexp = regexp.MustCompile(`(?s)\{.*\}`)
)

type rssFeed struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type papersOutput struct {
	GeneratedAt   string  `json:"generatedAt"`
	SourceFeedURL string  `json:"sourceFeedUrl"`
	Papers        []paper `json:"papers"`
}

type paper struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	GUID        string    `json:"guid"`
	PublishedAt string    `json:"publishedAt"`
	Authors     []string  `json:"authors"`
	Institution string    `json:"institution,omitempty"`
	ArxivURL    string    `json:"arxivUrl,omitempty"`
	AlphaXivURL string    `json:"alphaxivUrl,omitempty"`
	PDFURL      string    `json:"pdfUrl,omitempty"`
	Source      paperText `json:"source"`
	Japanese    paperText `json:"japanese"`
	Tags        []string  `json:"tags"`
}

type paperText struct {
	Title       string `json:"title"`
	Summary     string `json:"summary,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

type generatedPaper struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Explanation string   `json:"explanation"`
	Tags        []string `json:"tags"`
}

type chatRequest struct {
	Messages           []chatMessage  `json:"messages"`
	Temperature        float64        `json:"temperature"`
	Stream             bool           `json:"stream"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	ResponseFormat     responseFormat `json:"response_format,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type responseFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(ctx context.Context) error {
	items, err := fetchFeed(ctx, feedURL)
	if err != nil {
		return fmt.Errorf("fetch feed: %w", err)
	}

	llamaURL := os.Getenv("LLAMA_CPP_BASE_URL")
	if llamaURL == "" {
		llamaURL = defaultLLMBase
	}
	llamaURL = strings.TrimRight(llamaURL, "/")
	log.Printf("using llama.cpp at %s", llamaURL)

	schema, err := loadJSONSchema(envOrDefault("LLAMA_CPP_SCHEMA_PATH", defaultSchema))
	if err != nil {
		return fmt.Errorf("load JSON schema: %w", err)
	}

	llamaClient := &LlamaCppClient{
		BaseURL: llamaURL,
		Schema:  schema,
		HTTP:    &http.Client{Timeout: requestTimeout},
	}

	papers := make([]paper, 0, len(items))
	for i, item := range items {
		p, err := paperFromRSSItem(item)
		if err != nil {
			log.Printf("skip item %d: %v", i+1, err)
			continue
		}

		log.Printf("generate Japanese explanation %d/%d: %s", i+1, len(items), p.Source.Title)
		generated, err := llamaClient.GenerateJapanese(ctx, p)
		if err != nil {
			return fmt.Errorf("generate Japanese explanation for %q: %w", p.Source.Title, err)
		}

		p.Japanese = paperText{
			Title:       generated.Title,
			Summary:     generated.Summary,
			Explanation: generated.Explanation,
		}
		p.Tags = generated.Tags
		papers = append(papers, p)
	}

	out := papersOutput{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		SourceFeedURL: feedURL,
		Papers:        papers,
	}

	if err := writeJSON(outputPath, out); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	log.Printf("wrote %s with %d papers", outputPath, len(papers))
	return nil
}

func fetchFeed(ctx context.Context, url string) ([]rssItem, error) {
	client := &http.Client{Timeout: feedHTTPTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hf-papers-ja/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var feed rssFeed
	decoder := xml.NewDecoder(resp.Body)
	if err := decoder.Decode(&feed); err != nil {
		return nil, err
	}
	if len(feed.Channel.Items) == 0 {
		return nil, errors.New("feed has no items")
	}
	return feed.Channel.Items, nil
}

func paperFromRSSItem(item rssItem) (paper, error) {
	title := cleanText(item.Title)
	link := strings.TrimSpace(item.Link)
	guid := strings.TrimSpace(item.GUID)
	if title == "" {
		return paper{}, errors.New("missing title")
	}
	if link == "" {
		return paper{}, fmt.Errorf("%q missing link", title)
	}
	if guid == "" {
		guid = link
	}

	publishedAt := parseRSSDate(item.PubDate)
	metadata := extractDescriptionMetadata(item.Description)

	return paper{
		ID:          paperID(guid, link),
		URL:         link,
		GUID:        guid,
		PublishedAt: publishedAt,
		Authors:     metadata.Authors,
		Institution: metadata.Institution,
		ArxivURL:    metadata.ArxivURL,
		AlphaXivURL: alphaXivURL(metadata.ArxivURL),
		PDFURL:      metadata.PDFURL,
		Source: paperText{
			Title:       title,
			Summary:     metadata.Summary,
			Explanation: metadata.Abstract,
		},
	}, nil
}

type descriptionMetadata struct {
	Authors     []string
	Institution string
	ArxivURL    string
	PDFURL      string
	Summary     string
	Abstract    string
}

func extractDescriptionMetadata(description string) descriptionMetadata {
	meta := descriptionMetadata{}

	paragraphs := paragraphRegexp.FindAllStringSubmatch(description, -1)
	if len(paragraphs) > 0 {
		first := cleanHTML(paragraphs[0][1])
		for _, field := range strings.Split(first, "|") {
			field = strings.TrimSpace(field)
			switch {
			case strings.HasPrefix(field, "Institution:"):
				meta.Institution = strings.TrimSpace(strings.TrimPrefix(field, "Institution:"))
			case strings.HasPrefix(field, "Authors:"):
				authors := strings.TrimSpace(strings.TrimPrefix(field, "Authors:"))
				meta.Authors = splitCommaList(authors)
			}
		}
	}

	if match := arxivAbsRegexp.FindString(description); match != "" {
		meta.ArxivURL = match
	}
	if match := arxivPDFRegexp.FindString(description); match != "" {
		meta.PDFURL = match
	}
	if meta.PDFURL != "" && !strings.HasSuffix(meta.PDFURL, ".pdf") {
		meta.PDFURL += ".pdf"
	}

	meta.Summary = firstParagraphAfter(summaryBlockRE, description)
	meta.Abstract = firstParagraphAfter(abstractBlockRE, description)
	return meta
}

func firstParagraphAfter(blockRegexp *regexp.Regexp, s string) string {
	block := blockRegexp.FindStringSubmatch(s)
	if len(block) < 2 {
		return ""
	}
	paragraph := paragraphRegexp.FindStringSubmatch(block[1])
	if len(paragraph) < 2 {
		return cleanHTML(block[1])
	}
	return cleanHTML(paragraph[1])
}

type LlamaCppClient struct {
	BaseURL string
	Schema  map[string]any
	HTTP    *http.Client
}

func (c *LlamaCppClient) GenerateJapanese(ctx context.Context, p paper) (generatedPaper, error) {
	prompt := buildPrompt(p)
	reqBody := chatRequest{
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "あなたは機械学習論文を日本語で正確に解説する編集者です。誇張せず、与えられた情報だけに基づいてください。必ずJSONだけを返してください。",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: 0.2,
		Stream:      false,
		MaxTokens:   900,
		ResponseFormat: responseFormat{
			Type:   "json_schema",
			Schema: c.Schema,
		},
		ChatTemplateKwargs: map[string]any{
			"enable_thinking": false,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return generatedPaper{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return generatedPaper{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return generatedPaper{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return generatedPaper{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return generatedPaper{}, fmt.Errorf("llm status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var chat chatResponse
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return generatedPaper{}, err
	}
	if chat.Error != nil && chat.Error.Message != "" {
		return generatedPaper{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return generatedPaper{}, errors.New("llm returned no choices")
	}

	generated, err := parseGeneratedPaper(chat.Choices[0].Message.Content)
	if err != nil {
		return generatedPaper{}, err
	}
	generated.normalize(p)
	return generated, nil
}

func buildPrompt(p paper) string {
	abstract := p.Source.Explanation
	if len([]rune(abstract)) > maxAbstractChars {
		runes := []rune(abstract)
		abstract = string(runes[:maxAbstractChars])
	}

	input := struct {
		Title       string   `json:"title"`
		Authors     []string `json:"authors"`
		Summary     string   `json:"summary"`
		Explanation string   `json:"explanation"`
		URL         string   `json:"url"`
	}{
		Title:       p.Source.Title,
		Authors:     p.Authors,
		Summary:     p.Source.Summary,
		Explanation: abstract,
		URL:         p.URL,
	}
	raw, _ := json.MarshalIndent(input, "", "  ")

	return `次の論文情報を日本語サイト向けに解説してください。

返答は以下のJSONオブジェクトだけにしてください。
{
  "title": "日本語タイトル",
  "summary": "2文以内の短い日本語要約",
  "explanation": "背景、手法、重要性がわかる日本語解説。3から5文。",
  "tags": ["日本語タグ", "最大5個"]
}

論文情報:
` + string(raw)
}

func parseGeneratedPaper(content string) (generatedPaper, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	if !strings.HasPrefix(content, "{") {
		content = jsonObjectRegexp.FindString(content)
	}
	if content == "" {
		return generatedPaper{}, errors.New("llm response did not contain a JSON object")
	}

	var generated generatedPaper
	if err := json.Unmarshal([]byte(content), &generated); err != nil {
		return generatedPaper{}, err
	}
	return generated, nil
}

func (g *generatedPaper) normalize(p paper) {
	g.Title = cleanText(g.Title)
	g.Summary = cleanText(g.Summary)
	g.Explanation = cleanText(g.Explanation)
	g.Tags = cleanTags(g.Tags)

	if g.Title == "" {
		g.Title = p.Source.Title
	}
	if g.Summary == "" {
		g.Summary = p.Source.Summary
	}
	if g.Explanation == "" {
		g.Explanation = p.Source.Explanation
	}
}

func cleanTags(tags []string) []string {
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.Trim(tag, " #\t\r\n")
		tag = cleanText(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		cleaned = append(cleaned, tag)
		if len(cleaned) == 5 {
			break
		}
	}
	return cleaned
}

func writeJSON(filename string, data any) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

func loadJSONSchema(filename string) (map[string]any, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	log.Printf("loaded JSON schema from %s", filename)
	return schema, nil
}

func parseRSSDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func paperID(guid, link string) string {
	candidate := strings.TrimSpace(guid)
	if candidate == "" {
		candidate = link
	}
	candidate = strings.TrimRight(candidate, "/")
	id := path.Base(candidate)
	id = strings.TrimSpace(id)
	if id == "." || id == "/" || id == "" {
		return strings.NewReplacer("https://", "", "http://", "", "/", "-", "?", "-", "&", "-").Replace(candidate)
	}
	return id
}

func alphaXivURL(arxivURL string) string {
	arxivURL = strings.TrimSpace(arxivURL)
	if arxivURL == "" {
		return ""
	}
	arxivURL = strings.Replace(arxivURL, "https://www.arxiv.org/", "https://www.alphaxiv.org/", 1)
	arxivURL = strings.Replace(arxivURL, "https://arxiv.org/", "https://www.alphaxiv.org/", 1)
	return arxivURL
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = cleanText(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func cleanHTML(value string) string {
	value = tagRegexp.ReplaceAllString(value, " ")
	return cleanText(value)
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	value = strings.ReplaceAll(value, "\u00a0", " ")
	value = spaceRegexp.ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
