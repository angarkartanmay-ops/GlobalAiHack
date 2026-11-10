// Package tavily is a client for the Tavily Search and Extract APIs.
//
// It has two transports with the same request and response shapes:
//
//   - Relay: calls go to Bifrost at /relay/<name>/search. The gateway holds
//     the Tavily key, charges the call to Heimdall's virtual key and records
//     it in Relay Logs. This is the default.
//   - Direct: calls go straight to https://api.tavily.com with a bearer key,
//     for gateways that do not have relays configured.
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// SearchRequest mirrors POST /search. Zero values are omitted.
type SearchRequest struct {
	Query             string   `json:"query"`
	SearchDepth       string   `json:"search_depth,omitempty"` // basic, advanced
	Topic             string   `json:"topic,omitempty"`        // general, news, finance
	TimeRange         string   `json:"time_range,omitempty"`   // day, week, month, year
	MaxResults        int      `json:"max_results,omitempty"`
	IncludeDomains    []string `json:"include_domains,omitempty"`
	ExcludeDomains    []string `json:"exclude_domains,omitempty"`
	IncludeAnswer     bool     `json:"include_answer,omitempty"`
	IncludeRawContent string   `json:"include_raw_content,omitempty"` // markdown, text
}

// SearchResult is one hit.
type SearchResult struct {
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Content       string  `json:"content"`
	RawContent    string  `json:"raw_content"`
	Score         float64 `json:"score"`
	PublishedDate string  `json:"published_date"`
}

// Text returns the fullest text Tavily returned for the hit.
func (r SearchResult) Text() string {
	if strings.TrimSpace(r.RawContent) != "" {
		return r.RawContent
	}
	return r.Content
}

// SearchResponse mirrors the /search response.
type SearchResponse struct {
	Query        string         `json:"query"`
	Answer       string         `json:"answer"`
	Results      []SearchResult `json:"results"`
	ResponseTime any            `json:"response_time"`
}

// ExtractRequest mirrors POST /extract.
type ExtractRequest struct {
	URLs         []string `json:"urls"`
	ExtractDepth string   `json:"extract_depth,omitempty"` // basic, advanced
	Format       string   `json:"format,omitempty"`        // markdown, text
}

// ExtractResult is the content of one URL.
type ExtractResult struct {
	URL        string `json:"url"`
	RawContent string `json:"raw_content"`
}

// ExtractResponse mirrors the /extract response.
type ExtractResponse struct {
	Results       []ExtractResult `json:"results"`
	FailedResults []struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	} `json:"failed_results"`
}

// Client is what the radar and the freshness checker depend on.
type Client interface {
	Search(ctx context.Context, req SearchRequest) (*SearchResponse, error)
	Extract(ctx context.Context, req ExtractRequest) (*ExtractResponse, error)
	Mode() string
	Counts() (searches, extracts int64)
}

// Error is a non-2xx answer from Tavily (or from the gateway relaying to it).
type Error struct {
	Status int
	Detail string
}

func (e *Error) Error() string {
	hint := ""
	switch e.Status {
	case 401:
		hint = " (invalid or missing Tavily API key)"
	case 429:
		hint = " (Tavily rate limit reached)"
	case 432, 433:
		hint = " (Tavily plan or credit limit reached)"
	case 403:
		hint = " (not allowed: check the relay grant on Heimdall's virtual key)"
	}
	return fmt.Sprintf("tavily %d%s: %s", e.Status, hint, e.Detail)
}

type counters struct{ searches, extracts atomic.Int64 }

func (c *counters) Counts() (int64, int64) { return c.searches.Load(), c.extracts.Load() }

// ---------------------------------------------------------------------------
// Direct transport
// ---------------------------------------------------------------------------

// Direct calls the Tavily API with a bearer key.
type Direct struct {
	counters
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewDirect returns a client for the public Tavily API.
func NewDirect(apiKey, baseURL string) *Direct {
	if baseURL == "" {
		baseURL = "https://api.tavily.com"
	}
	return &Direct{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 90 * time.Second}}
}

func (d *Direct) Mode() string { return "direct" }

func (d *Direct) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("X-Client-Source", "heimdall")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("tavily %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return &Error{Status: resp.StatusCode, Detail: errorDetail(data)}
	}
	return json.Unmarshal(data, out)
}

func (d *Direct) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	d.searches.Add(1)
	var out SearchResponse
	if err := d.post(ctx, "/search", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (d *Direct) Extract(ctx context.Context, req ExtractRequest) (*ExtractResponse, error) {
	d.extracts.Add(1)
	var out ExtractResponse
	if err := d.post(ctx, "/extract", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// errorDetail pulls the message out of Tavily's {"detail":{"error":"..."}} body.
func errorDetail(body []byte) string {
	var env struct {
		Detail any `json:"detail"`
		Error  any `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil {
		switch d := env.Detail.(type) {
		case map[string]any:
			if s, ok := d["error"].(string); ok {
				return s
			}
		case string:
			return d
		}
		switch e := env.Error.(type) {
		case map[string]any:
			if s, ok := e["message"].(string); ok {
				return s
			}
		case string:
			return e
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// Relay transport
// ---------------------------------------------------------------------------

// Relayer is the part of the Bifrost client the relay transport needs.
type Relayer interface {
	Relay(ctx context.Context, relay, path string, body, out any) error
}

// ViaRelay sends Tavily calls through a Bifrost relay.
type ViaRelay struct {
	counters
	Gateway Relayer
	Name    string
	// AsError converts a gateway error into a status and message, so relay
	// failures read the same as direct ones.
	AsError func(error) (status int, detail string, ok bool)
}

// NewViaRelay returns a client that reaches Tavily through the named relay.
func NewViaRelay(gw Relayer, name string, asError func(error) (int, string, bool)) *ViaRelay {
	return &ViaRelay{Gateway: gw, Name: name, AsError: asError}
}

func (v *ViaRelay) Mode() string { return "relay:" + v.Name }

func (v *ViaRelay) wrap(err error) error {
	if err == nil {
		return nil
	}
	if v.AsError != nil {
		if status, detail, ok := v.AsError(err); ok {
			return &Error{Status: status, Detail: detail}
		}
	}
	return err
}

func (v *ViaRelay) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	v.searches.Add(1)
	var out SearchResponse
	if err := v.Gateway.Relay(ctx, v.Name, "/search", req, &out); err != nil {
		return nil, v.wrap(err)
	}
	return &out, nil
}

func (v *ViaRelay) Extract(ctx context.Context, req ExtractRequest) (*ExtractResponse, error) {
	v.extracts.Add(1)
	var out ExtractResponse
	if err := v.Gateway.Relay(ctx, v.Name, "/extract", req, &out); err != nil {
		return nil, v.wrap(err)
	}
	return &out, nil
}
