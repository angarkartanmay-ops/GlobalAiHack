// Package mcpserver exposes Heimdall as a Model Context Protocol server.
//
// Registered as an MCP client in Bifrost, it gives every agent behind the
// gateway three tools: list the models that are about to be retired, look up
// one model's lifecycle evidence, and check whether an answer is still true.
// The transport is MCP's streamable HTTP with plain JSON responses.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/freshness"
)

// Backend is what the tools call into.
type Backend interface {
	Exposures(ctx context.Context) ([]core.Exposure, *core.Inventory)
	CheckModel(ctx context.Context, model string) (core.Finding, error)
	CheckAnswer(ctx context.Context, req freshness.Request) (core.FreshnessReport, error)
}

const protocolVersion = "2025-03-26"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var tools = []tool{
	{
		Name:        "heimdall_list_sunsets",
		Description: "List the AI models this gateway is serving that their providers have scheduled for retirement or already shut down, with the shutdown date, days left, share of traffic, the applications affected and the recommended replacement. Use this before recommending or configuring a model.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "heimdall_check_model",
		Description: "Look up the lifecycle status of one model (provider/model) from the provider's own documentation, retrieved live. Returns the status, shutdown date and replacement, each with the quoted source text and URL it rests on.",
		InputSchema: map[string]any{"type": "object", "required": []string{"model"},
			"properties": map[string]any{"model": map[string]any{"type": "string", "description": "Model as provider/model, for example openai/gpt-4o"}}},
	},
	{
		Name:        "heimdall_check_answer",
		Description: "Check whether an answer is still true today. Finds the claims in the answer that can go out of date (versions, prices, who holds a role, availability, statistics), checks each against live web search, and reports which are supported, changed or unverifiable, with source links.",
		InputSchema: map[string]any{"type": "object", "required": []string{"answer"},
			"properties": map[string]any{
				"answer":   map[string]any{"type": "string", "description": "The answer text to check"},
				"question": map[string]any{"type": "string", "description": "The question the answer responds to, if known"},
			}},
	},
}

// New returns the MCP HTTP handler.
func New(b Backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			return
		default:
			// No server-initiated stream is offered.
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		trimmed := strings.TrimSpace(string(body))
		var reqs []rpcRequest
		batch := strings.HasPrefix(trimmed, "[")
		if batch {
			err = json.Unmarshal(body, &reqs)
		} else {
			var one rpcRequest
			err = json.Unmarshal(body, &one)
			reqs = []rpcRequest{one}
		}
		if err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			return
		}
		var resps []rpcResponse
		for _, req := range reqs {
			if resp, ok := handle(r.Context(), b, req); ok {
				resps = append(resps, resp)
			}
		}
		switch {
		case len(resps) == 0:
			w.WriteHeader(http.StatusAccepted) // notifications only
		case batch:
			writeRPC(w, resps)
		default:
			writeRPC(w, resps[0])
		}
	})
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func handle(ctx context.Context, b Backend, req rpcRequest) (rpcResponse, bool) {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	isNotification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := protocolVersion
		if p.ProtocolVersion != "" {
			v = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "heimdall", "version": "1.0.0"},
			"instructions":    "Heimdall watches this AI gateway for models that are being retired and checks answers for stale facts. Evidence is always quoted from a retrieved source.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "invalid params"}
			break
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		text, err := call(cctx, b, p.Name, p.Arguments)
		cancel()
		if err != nil {
			resp.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}
		} else {
			resp.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": false}
		}
	default:
		if isNotification {
			return resp, false
		}
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp, !isNotification
}

func call(ctx context.Context, b Backend, name string, args json.RawMessage) (string, error) {
	switch name {
	case "heimdall_list_sunsets":
		exposures, inv := b.Exposures(ctx)
		return renderSunsets(exposures, inv), nil
	case "heimdall_check_model":
		var a struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(args, &a)
		if !strings.Contains(a.Model, "/") {
			return "", fmt.Errorf("model must be given as provider/model")
		}
		f, err := b.CheckModel(ctx, a.Model)
		if err != nil {
			return "", err
		}
		return renderFinding(f), nil
	case "heimdall_check_answer":
		var a struct {
			Answer   string `json:"answer"`
			Question string `json:"question"`
		}
		_ = json.Unmarshal(args, &a)
		if strings.TrimSpace(a.Answer) == "" {
			return "", fmt.Errorf("answer is required")
		}
		rep, err := b.CheckAnswer(ctx, freshness.Request{Question: a.Question, Answer: a.Answer, Source: "mcp", Run: "mcp"})
		if err != nil {
			return "", err
		}
		return renderReport(rep), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func renderSunsets(exposures []core.Exposure, inv *core.Inventory) string {
	var b strings.Builder
	n := 0
	for _, ex := range exposures {
		f := ex.Finding
		if f == nil || (f.Status != core.StatusDeprecated && f.Status != core.StatusRetired) {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s: %s", ex.ID(), f.Status)
		if f.ShutdownDate != "" {
			fmt.Fprintf(&b, ", shutdown %s", f.ShutdownDate)
			if ex.DaysLeft != nil {
				fmt.Fprintf(&b, " (%d days)", *ex.DaysLeft)
			}
		}
		fmt.Fprintf(&b, ", risk %s, confidence %s", ex.Risk, f.Confidence)
		if t := ex.Traffic; t != nil {
			fmt.Fprintf(&b, ", %.0f%% of gateway requests", 100*t.RequestShare)
			var names []string
			for _, c := range t.Consumers {
				names = append(names, c.Name)
			}
			if len(names) > 0 {
				fmt.Fprintf(&b, " from %s", strings.Join(names, ", "))
			}
		}
		if f.Replacement != "" {
			fmt.Fprintf(&b, ". Replacement named by the provider: %s", f.Replacement)
		}
		for _, e := range f.Evidence {
			if e.URL != "" && (e.Applies == "exact" || e.Applies == "alias") {
				fmt.Fprintf(&b, ". Source: %s", e.URL)
				break
			}
		}
		b.WriteString("\n")
	}
	if n == 0 {
		if inv == nil || len(inv.Items) == 0 {
			return "No traffic has been observed on this gateway yet, so there is nothing to check."
		}
		return fmt.Sprintf("None of the %d models currently serving traffic on this gateway has a retirement notice.", len(inv.Items))
	}
	return fmt.Sprintf("%d model(s) serving traffic on this gateway are scheduled for retirement or already shut down:\n%s", n, b.String())
}

func renderFinding(f core.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: status %s", f.ID(), f.Status)
	if f.ShutdownDate != "" {
		fmt.Fprintf(&b, ", shutdown date %s", f.ShutdownDate)
	}
	if f.Replacement != "" {
		fmt.Fprintf(&b, ", replacement %s (%s)", f.Replacement, f.ReplacementBasis)
	}
	fmt.Fprintf(&b, ", confidence %s.\n", f.Confidence)
	if len(f.Evidence) > 0 {
		b.WriteString("Evidence (quoted from retrieved sources):\n")
		for _, e := range f.Evidence {
			src := e.URL
			if src == "" {
				src = e.Title
			}
			fmt.Fprintf(&b, "- %q - %s\n", e.Quote, src)
		}
	}
	if len(f.Inference) > 0 {
		b.WriteString("Inference (not from a source):\n")
		for _, s := range f.Inference {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	if f.Error != "" {
		fmt.Fprintf(&b, "Check error: %s\n", f.Error)
	}
	return b.String()
}

func renderReport(rep core.FreshnessReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Checked %d time-sensitive claim(s): %d supported, %d changed, %d unverifiable.\n", rep.Supported+rep.Changed+rep.Unverified, rep.Supported, rep.Changed, rep.Unverified)
	for _, c := range rep.Claims {
		if !c.Checked {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s", strings.ToUpper(c.Verdict), c.Text)
		if c.Verdict == core.VerdictChanged && c.CurrentValue != "" {
			fmt.Fprintf(&b, " Now: %s.", c.CurrentValue)
		}
		if len(c.Evidence) > 0 {
			fmt.Fprintf(&b, " Evidence: %q (%s)", c.Evidence[0].Quote, c.Evidence[0].URL)
		}
		b.WriteString("\n")
	}
	if rep.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", rep.Error)
	}
	return b.String()
}
