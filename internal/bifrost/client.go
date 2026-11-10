// Package bifrost is a client for the Bifrost gateway's public HTTP surface.
//
// Heimdall extends the gateway only through endpoints any operator can call
// on a stock install:
//
//	inference   POST /v1/chat/completions, GET /v1/models, GET /v1/models/{id}
//	logs        GET  /api/logs, /api/logs/{id}, /api/logs/rankings[/by-dimension]
//	routing     CRUD /api/routing/rules
//	relays      POST /relay/{name}/{path}
//
// Inference and relay calls authenticate with a virtual key (x-bf-vk), so the
// gateway's budgets and rate limits apply to Heimdall like to any other
// consumer. Management calls use the dashboard's admin credentials.
package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TagHeader marks every request Heimdall sends. Bifrost captures x-bf-lh-*
// headers into log metadata, which lets Heimdall keep its own traffic out of
// the inventory and out of rehearsal samples.
const (
	TagHeader     = "x-bf-lh-heimdall"
	TagKey        = "heimdall" // metadata key Bifrost derives from TagHeader
	KindHeader    = "x-bf-lh-heimdall-kind"
	RunHeader     = "x-bf-lh-heimdall-run"
	virtualKeyHdr = "x-bf-vk"
)

// Client talks to one Bifrost gateway.
type Client struct {
	BaseURL    string
	VirtualKey string
	AdminUser  string
	AdminPass  string
	HTTP       *http.Client
}

// New returns a client with sensible timeouts.
func New(baseURL, virtualKey, adminUser, adminPass string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		VirtualKey: virtualKey,
		AdminUser:  adminUser,
		AdminPass:  adminPass,
		HTTP:       &http.Client{Timeout: 120 * time.Second},
	}
}

// APIError is a non-2xx answer from the gateway.
type APIError struct {
	Status  int
	Type    string
	Code    string
	Message string
	Body    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = truncate(e.Body, 300)
	}
	parts := []string{fmt.Sprintf("bifrost %d", e.Status)}
	if e.Type != "" {
		parts = append(parts, e.Type)
	}
	if e.Code != "" {
		parts = append(parts, e.Code)
	}
	return strings.Join(parts, " ") + ": " + msg
}

// IsStatus reports whether err is an APIError with the given HTTP status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// IsBudget reports whether the gateway refused the call because a budget or
// rate limit attached to the virtual key is exhausted.
func IsBudget(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	s := strings.ToLower(ae.Type + " " + ae.Code + " " + ae.Message)
	return ae.Status == 402 || ae.Status == 429 && (strings.Contains(s, "budget") || strings.Contains(s, "limit")) ||
		strings.Contains(s, "budget_exceeded") || strings.Contains(s, "budget exceeded")
}

func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status, Body: string(body)}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ExtraFields struct {
			ErrorType string `json:"error_type"`
		} `json:"extra_fields"`
	}
	if json.Unmarshal(body, &env) == nil {
		e.Message = env.Error.Message
		e.Type = firstNonEmpty(env.Type, env.Error.Type, env.ExtraFields.ErrorType)
		if env.Error.Code != nil {
			e.Code = fmt.Sprint(env.Error.Code)
		}
	}
	return e
}

type authMode int

const (
	authNone authMode = iota
	authVK
	authAdmin
)

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, mode authMode, hdr map[string]string, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch mode {
	case authVK:
		if c.VirtualKey != "" {
			req.Header.Set(virtualKeyHdr, c.VirtualKey)
		}
		// Everything Heimdall sends under its own key is tagged, including
		// model metadata lookups, so none of it is mistaken for demand.
		req.Header.Set(TagHeader, "1")
	case authAdmin:
		if c.AdminUser != "" || c.AdminPass != "" {
			req.SetBasicAuth(c.AdminUser, c.AdminPass)
		}
	}
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("bifrost %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("bifrost %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("bifrost %s %s: decode: %w (body: %s)", method, path, err, truncate(string(data), 200))
	}
	return nil
}

// Health checks that the gateway is up.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/health", nil, nil, authNone, nil, nil)
}

// ---------------------------------------------------------------------------
// Inference
// ---------------------------------------------------------------------------

// ChatRequest is an OpenAI-compatible chat completion request. Extra carries
// any further parameters verbatim (used when replaying logged requests).
type ChatRequest struct {
	Model     string
	Messages  json.RawMessage
	Fallbacks []string
	Extra     map[string]json.RawMessage
}

// MarshalJSON flattens Extra into the request body.
func (r ChatRequest) MarshalJSON() ([]byte, error) {
	m := map[string]json.RawMessage{}
	for k, v := range r.Extra {
		m[k] = v
	}
	mb, _ := json.Marshal(r.Model)
	m["model"] = mb
	m["messages"] = r.Messages
	if len(r.Fallbacks) > 0 {
		fb, _ := json.Marshal(r.Fallbacks)
		m["fallbacks"] = fb
	}
	delete(m, "stream")
	return json.Marshal(m)
}

// Message is the assistant message of a completion.
type Message struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
	Refusal *string `json:"refusal"`
}

// Text returns the message content as plain text, flattening content parts.
func (m Message) Text() string { return ContentText(m.Content) }

// ContentText flattens an OpenAI message "content" value (string, null, or an
// array of typed parts) to plain text.
func ContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}

// Usage is token accounting for a completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is the part of a chat completion Heimdall reads.
type ChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		FinishReason string  `json:"finish_reason"`
		Message      Message `json:"message"`
	} `json:"choices"`
	Usage       Usage `json:"usage"`
	ExtraFields struct {
		Provider    string `json:"provider"`
		RoutingInfo struct {
			Provider   string `json:"provider"`
			Model      string `json:"model"`
			IsFallback bool   `json:"is_fallback"`
		} `json:"routing_info"`
	} `json:"extra_fields"`
	Elapsed time.Duration `json:"-"`
}

// First returns the first choice's message and finish reason.
func (r *ChatResponse) First() (Message, string) {
	if r == nil || len(r.Choices) == 0 {
		return Message{}, ""
	}
	return r.Choices[0].Message, r.Choices[0].FinishReason
}

// Tags label a Heimdall request in gateway logs.
type Tags struct {
	Kind string // radar, judge, replay, freshness, traffic
	Run  string
}

func (t Tags) headers() map[string]string {
	return map[string]string{TagHeader: "1", KindHeader: t.Kind, RunHeader: t.Run}
}

// Chat sends a chat completion through the gateway under Heimdall's virtual key.
func (c *Client) Chat(ctx context.Context, req ChatRequest, tags Tags) (*ChatResponse, error) {
	return c.chat(ctx, req, c.VirtualKey, tags.headers())
}

// ChatAs sends a chat completion under another virtual key and without
// Heimdall's log tag. Only the bundled sample-traffic generator uses it, to
// stand in for an application.
func (c *Client) ChatAs(ctx context.Context, req ChatRequest, virtualKey string, hdr map[string]string) (*ChatResponse, error) {
	return c.chat(ctx, req, virtualKey, hdr)
}

func (c *Client) chat(ctx context.Context, req ChatRequest, vk string, hdr map[string]string) (*ChatResponse, error) {
	h := map[string]string{}
	for k, v := range hdr {
		h[k] = v
	}
	if vk != "" {
		h[virtualKeyHdr] = vk
	}
	var out ChatResponse
	start := time.Now()
	if err := c.do(ctx, http.MethodPost, "/v1/chat/completions", nil, req, authNone, h, &out); err != nil {
		return nil, err
	}
	out.Elapsed = time.Since(start)
	return &out, nil
}

// ModelInfo is one entry of the gateway's model listing.
type ModelInfo struct {
	ID           string `json:"id"` // provider/model
	Created      int64  `json:"created"`
	OwnedBy      string `json:"owned_by"`
	ShutdownDate string `json:"shutdown_date"`
	Pricing      struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// PromptPrice and CompletionPrice return USD per token, or 0 when unknown.
func (m ModelInfo) PromptPrice() float64 { f, _ := strconv.ParseFloat(m.Pricing.Prompt, 64); return f }
func (m ModelInfo) CompletionPrice() float64 {
	f, _ := strconv.ParseFloat(m.Pricing.Completion, 64)
	return f
}

// ListModels returns every model the virtual key may use.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var out struct {
		Data []ModelInfo `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/models", nil, nil, authVK, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// RetrieveModel asks the provider, through the gateway, for one model's
// metadata. Providers that announce a retirement date in their API surface it
// as shutdown_date; most do not, which is why the radar also reads the web.
func (c *Client) RetrieveModel(ctx context.Context, id string) (*ModelInfo, error) {
	var out ModelInfo
	if err := c.do(ctx, http.MethodGet, "/v1/models/"+id, nil, nil, authVK, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// Ranking is one row of the gateway's per-model traffic aggregate.
type Ranking struct {
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	TotalRequests int     `json:"total_requests"`
	SuccessCount  int     `json:"success_count"`
	SuccessRate   float64 `json:"success_rate"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCost     float64 `json:"total_cost"`
	AvgLatency    float64 `json:"avg_latency"`
}

// LogFilter narrows log queries. Zero values are omitted.
type LogFilter struct {
	Providers      []string
	Models         []string
	Status         []string
	Objects        []string
	RoutingRuleIDs []string
	Start, End     time.Time
	Metadata       map[string]string
	Limit, Offset  int
	Order          string // "asc" or "desc" by timestamp; the gateway defaults to desc
}

func (f LogFilter) values() url.Values {
	q := url.Values{}
	set := func(k string, v []string) {
		if len(v) > 0 {
			q.Set(k, strings.Join(v, ","))
		}
	}
	set("providers", f.Providers)
	set("models", f.Models)
	set("status", f.Status)
	set("objects", f.Objects)
	set("routing_rule_ids", f.RoutingRuleIDs)
	if !f.Start.IsZero() {
		q.Set("start_time", f.Start.UTC().Format(time.RFC3339))
	}
	if !f.End.IsZero() {
		q.Set("end_time", f.End.UTC().Format(time.RFC3339))
	}
	for k, v := range f.Metadata {
		q.Set("metadata_"+k, v)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	if f.Order != "" {
		q.Set("sort_by", "timestamp")
		q.Set("order", f.Order)
	}
	return q
}

// Rankings returns traffic aggregated by provider and model.
func (c *Client) Rankings(ctx context.Context, f LogFilter) ([]Ranking, error) {
	var out struct {
		Rankings []Ranking `json:"rankings"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/logs/rankings", f.values(), nil, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return out.Rankings, nil
}

// DimensionRanking is one row of traffic aggregated by a consumer dimension.
type DimensionRanking struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	TotalRequests int     `json:"total_requests"`
	TotalCost     float64 `json:"total_cost"`
}

// RankingsBy aggregates traffic by a dimension such as "virtual_key" or "team".
func (c *Client) RankingsBy(ctx context.Context, dimension string, f LogFilter) ([]DimensionRanking, error) {
	q := f.values()
	q.Set("dimension", dimension)
	var out struct {
		Rankings []DimensionRanking `json:"rankings"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/logs/rankings/by-dimension", q, nil, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return out.Rankings, nil
}

// Log is one gateway log entry. The list endpoint returns a shortened form;
// LogDetail returns the full conversation and parameters.
type Log struct {
	ID              string                     `json:"id"`
	ParentRequestID *string                    `json:"parent_request_id"`
	Timestamp       time.Time                  `json:"timestamp"`
	Object          string                     `json:"object"`
	Provider        string                     `json:"provider"`
	Model           string                     `json:"model"`
	FallbackIndex   int                        `json:"fallback_index"`
	VirtualKeyID    *string                    `json:"virtual_key_id"`
	VirtualKeyName  *string                    `json:"virtual_key_name"`
	RoutingRuleID   *string                    `json:"routing_rule_id"`
	RoutingRuleName *string                    `json:"routing_rule_name"`
	Latency         float64                    `json:"latency"` // milliseconds
	Cost            float64                    `json:"cost"`
	Status          string                     `json:"status"`
	Stream          bool                       `json:"stream"`
	StopReason      string                     `json:"stop_reason"`
	ContentHidden   bool                       `json:"content_hidden"`
	Metadata        map[string]any             `json:"metadata"`
	InputHistory    json.RawMessage            `json:"input_history"`
	OutputMessage   *Message                   `json:"output_message"`
	Params          map[string]json.RawMessage `json:"params"`
	TokenUsage      *Usage                     `json:"token_usage"`
}

// IsHeimdall reports whether Heimdall itself sent this request.
func (l Log) IsHeimdall() bool {
	_, ok := l.Metadata[TagKey]
	return ok
}

// Consumer returns the virtual key name, or "" for unauthenticated traffic.
func (l Log) Consumer() string {
	if l.VirtualKeyName != nil {
		return *l.VirtualKeyName
	}
	return ""
}

// LogStats is the aggregate the logs endpoint returns alongside a page.
type LogStats struct {
	TotalRequests  int     `json:"total_requests"`
	SuccessRate    float64 `json:"success_rate"`
	AverageLatency float64 `json:"average_latency"`
	TotalCost      float64 `json:"total_cost"`
}

// LogPage is one page of logs.
type LogPage struct {
	Logs       []Log `json:"logs"`
	Pagination struct {
		TotalCount int `json:"total_count"`
	} `json:"pagination"`
	Stats LogStats `json:"stats"`
}

// Logs returns one page of log entries, newest first.
func (c *Client) Logs(ctx context.Context, f LogFilter) (*LogPage, error) {
	var out LogPage
	if err := c.do(ctx, http.MethodGet, "/api/logs", f.values(), nil, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LogDetail returns one log entry with its full request and response.
func (c *Client) LogDetail(ctx context.Context, id string) (*Log, error) {
	var out Log
	if err := c.do(ctx, http.MethodGet, "/api/logs/"+url.PathEscape(id), nil, nil, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Routing rules
// ---------------------------------------------------------------------------

// Target is one weighted destination of a routing rule.
type Target struct {
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Weight   float64 `json:"weight"`
}

// Rule is a Bifrost routing rule: a CEL match plus weighted targets and fallbacks.
type Rule struct {
	ID            string   `json:"id,omitempty"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Enabled       bool     `json:"enabled"`
	CELExpression string   `json:"cel_expression"`
	Targets       []Target `json:"targets"`
	Fallbacks     []string `json:"fallbacks"`
	Scope         string   `json:"scope"`
	Priority      int      `json:"priority"`
}

// ListRules returns all routing rules.
func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	var out struct {
		Rules []Rule `json:"rules"`
	}
	q := url.Values{"limit": {"500"}}
	if err := c.do(ctx, http.MethodGet, "/api/routing/rules", q, nil, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

// CreateRule creates a routing rule and returns it with its ID.
func (c *Client) CreateRule(ctx context.Context, r Rule) (*Rule, error) {
	var out struct {
		Rule Rule `json:"rule"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/routing/rules", nil, r, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return &out.Rule, nil
}

// UpdateRule replaces the mutable fields of a rule.
func (c *Client) UpdateRule(ctx context.Context, id string, r Rule) (*Rule, error) {
	var out struct {
		Rule Rule `json:"rule"`
	}
	r.ID = ""
	if err := c.do(ctx, http.MethodPut, "/api/routing/rules/"+url.PathEscape(id), nil, r, authAdmin, nil, &out); err != nil {
		return nil, err
	}
	return &out.Rule, nil
}

// DeleteRule removes a rule. A missing rule is not an error.
func (c *Client) DeleteRule(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/api/routing/rules/"+url.PathEscape(id), nil, nil, authAdmin, nil, nil)
	if IsStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// Relays
// ---------------------------------------------------------------------------

// Relay posts to a non-LLM API registered on the gateway as a relay. The
// gateway injects the stored upstream credential, checks the virtual key's
// relay grant and budget, and records the call in Relay Logs.
func (c *Client) Relay(ctx context.Context, relay, path string, body, out any) error {
	p := "/relay/" + url.PathEscape(relay) + "/" + strings.TrimLeft(path, "/")
	return c.do(ctx, http.MethodPost, p, nil, body, authVK, nil, out)
}

// AdminReachable reports whether the management API accepts the credentials.
func (c *Client) AdminReachable(ctx context.Context) error {
	_, err := c.Logs(ctx, LogFilter{Limit: 1})
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
