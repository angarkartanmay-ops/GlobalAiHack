package sim

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
)

// System prompts of the simulated applications. The simulated models key
// their behaviour on these, and the traffic generator sends them.
const (
	SysSupport = "You are Northpost's support assistant. Answer customer questions about parcels, pricing and the Northpost API in two or three sentences."
	SysExtract = "Extract a JSON object with the keys order_id, intent and urgency from the customer message. Reply with JSON only."
	SysTools   = "You are Northpost's order assistant. Use the tools to look up orders and parcels before answering."
	SysRewrite = "Rewrite the search query for the parcel search index. Reply with a JSON object with the keys query and filters."
)

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

func (m chatMessage) text() string { return bifrost.ContentText(m.Content) }

type chatRequest struct {
	Model    string            `json:"model"`
	Messages []chatMessage     `json:"messages"`
	Tools    []json.RawMessage `json:"tools"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type reply struct {
	content   string
	toolCalls []toolCall
}

// Handler serves the simulated provider APIs and the simulated web:
//
//	/aurora/v1/...   OpenAI-compatible API of the provider "aurora"
//	/lumen/v1/...    OpenAI-compatible API of the provider "lumen"
//	/tavily/search   Tavily-compatible search over the simulated web
//	/tavily/extract  Tavily-compatible page extraction
func (w *World) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, p := range []string{ProviderAurora, ProviderLumen} {
		mux.HandleFunc("GET /"+p+"/v1/models", w.auth(w.handleModels(p)))
		mux.HandleFunc("GET /"+p+"/v1/models/{id}", w.auth(w.handleModel(p)))
		mux.HandleFunc("POST /"+p+"/v1/chat/completions", w.auth(w.handleChat(p)))
	}
	mux.HandleFunc("POST /tavily/search", w.auth(w.handleSearch))
	mux.HandleFunc("POST /tavily/extract", w.auth(w.handleExtract))
	return mux
}

// auth requires the bearer key the gateway is configured with, so the
// simulator is not an open endpoint when Heimdall is reachable publicly.
func (w *World) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if w.Key != "" && r.Header.Get("Authorization") != "Bearer "+w.Key {
			writeJSON(rw, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "invalid simulator key", "type": "authentication_error"}})
			return
		}
		next(rw, r)
	}
}

func (w *World) handleModels(provider string) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		data := []map[string]any{}
		for _, id := range ModelIDs() {
			if p, name, _ := strings.Cut(id, "/"); p == provider {
				data = append(data, map[string]any{"id": name, "object": "model", "created": w.T0.AddDate(0, -8, 0).Unix(), "owned_by": provider})
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"object": "list", "data": data})
	}
}

func (w *World) handleModel(provider string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		t, ok := models[id]
		if !ok || t.provider != provider {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "The model `" + id + "` does not exist", "type": "invalid_request_error", "code": "model_not_found"}})
			return
		}
		out := map[string]any{"id": id, "object": "model", "created": w.T0.AddDate(0, -8, 0).Unix(), "owned_by": provider}
		if t.apiShutdown {
			out["shutdown_date"] = iso(w.MiniShutdown())
		}
		writeJSON(rw, http.StatusOK, out)
	}
}

func hashOf(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32() & 0x7fffffff)
}

func every(n, h int) bool { return n > 0 && h%n == 0 }

func (w *World) handleChat(provider string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid JSON body", "type": "invalid_request_error"}})
			return
		}
		t, ok := models[req.Model]
		if !ok || t.provider != provider {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "The model `" + req.Model + "` does not exist", "type": "invalid_request_error", "code": "model_not_found"}})
			return
		}
		if w.Retired(req.Model) {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": map[string]any{
				"message": "The model `" + req.Model + "` has been retired and is no longer available. See https://docs.aurora-ai.example/models/deprecations",
				"type":    "invalid_request_error", "code": "model_not_found"}})
			return
		}

		var system, lastUser string
		var last chatMessage
		for _, m := range req.Messages {
			switch m.Role {
			case "system":
				if system == "" {
					system = m.text()
				}
			case "user":
				lastUser = m.text()
			}
			last = m
		}

		var rep reply
		if task := taskOf(system); task != "" {
			rep.content = w.analyst(task, lastUser)
		} else {
			rep = w.application(req, t, system, lastUser, last)
			if !w.Fast {
				span := t.latency[1] - t.latency[0]
				time.Sleep(time.Duration(t.latency[0]+rand.Intn(span+1)) * time.Millisecond)
			}
		}

		promptChars := 0
		for _, m := range req.Messages {
			promptChars += len(m.text()) + 8
		}
		outChars := len(rep.content)
		for _, tc := range rep.toolCalls {
			outChars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
		msg := map[string]any{"role": "assistant", "content": rep.content}
		finish := "stop"
		if len(rep.toolCalls) > 0 {
			msg["content"] = nil
			msg["tool_calls"] = rep.toolCalls
			finish = "tool_calls"
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"id": fmt.Sprintf("chatcmpl-sim-%d", time.Now().UnixNano()), "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": promptChars/4 + 1, "completion_tokens": outChars/4 + 1, "total_tokens": (promptChars+outChars)/4 + 2},
		})
	}
}

var (
	orderRe    = regexp.MustCompile(`\b(\d{5})\b`)
	trackingRe = regexp.MustCompile(`\bNP\d{8}\b`)
	weightRe   = regexp.MustCompile(`(?i)\b(\d+)\s?kg\b`)
)

// application produces what a simulated application model would answer.
func (w *World) application(req chatRequest, t traits, system, user string, last chatMessage) reply {
	h := hashOf(user)
	low := strings.ToLower(user)
	switch {
	case strings.HasPrefix(system, "Extract a JSON object"):
		return reply{content: extractJSON(t, user, low, h)}
	case strings.HasPrefix(system, "Rewrite the search query"):
		return reply{content: rewriteQuery(low)}
	case len(req.Tools) > 0:
		return orderAssistant(t, user, last, h)
	case strings.HasPrefix(system, "You are Northpost's support assistant"):
		if every(t.refuseEvery, h) {
			return reply{content: "I'm sorry, but I can't help with that request."}
		}
		return reply{content: styled(t, supportAnswer(t, low))}
	}
	return reply{content: "Here is a short answer to your message: " + strings.TrimSpace(user)}
}

func supportAnswer(t traits, low string) string {
	switch {
	case strings.Contains(low, "api") && (strings.Contains(low, "version") || strings.Contains(low, "integrat")):
		return fmt.Sprintf("New integrations should use Northpost API %s, which is the current version. The developer portal has the full reference.", t.facts["api"])
	case strings.Contains(low, "weekend"):
		return fmt.Sprintf("Weekend delivery carries a surcharge of %s per parcel. Weekday deliveries have no surcharge.", t.facts["surcharge"])
	case strings.Contains(low, "ceo") || strings.Contains(low, "chief executive") || strings.Contains(low, "who runs"):
		return fmt.Sprintf("The Chief Executive Officer of Northpost is %s.", t.facts["ceo"])
	case strings.Contains(low, "how long") || strings.Contains(low, "delivery time") || strings.Contains(low, "standard delivery"):
		return fmt.Sprintf("Standard delivery takes %s. Express delivery arrives on the next business day when booked before noon.", t.facts["delivery"])
	case strings.Contains(low, "return"):
		if t.dropReturnWindow {
			return "You can return a parcel by starting a return from the tracking page, which gives you a prepaid label."
		}
		return "You can return a parcel within 30 days of delivery. Start the return from the tracking page to get a prepaid label."
	case strings.Contains(low, "track"):
		return "Open the tracking page and enter your tracking number to see the latest scan. Scans update within an hour of each handover."
	}
	return "I can help with parcels, pricing and the Northpost API. Could you tell me a little more about what you need?"
}

// styled rewords an answer the way different model generations do, without
// changing its facts.
func styled(t traits, s string) string {
	switch t.style {
	case "concise":
		s = strings.Replace(s, "New integrations should use", "Use", 1)
		s = strings.Replace(s, ", which is the current version", ", the current version", 1)
		s = strings.Replace(s, "carries a surcharge of", "has a surcharge of", 1)
		return s
	case "verbose":
		return "Thanks for reaching out! " + s + " Is there anything else I can help you with?"
	}
	return s
}

func extractJSON(t traits, user, low string, h int) string {
	order := ""
	if m := orderRe.FindString(user); m != "" {
		order = m
	}
	intent := "general_question"
	switch {
	case strings.Contains(low, "damaged") || strings.Contains(low, "broken"):
		intent = "damaged_item"
	case strings.Contains(low, "refund"):
		intent = "refund_request"
	case strings.Contains(low, "cancel"):
		intent = "cancellation"
	case strings.Contains(low, "where") || strings.Contains(low, "late") || strings.Contains(low, "not arrived") || strings.Contains(low, "hasn't arrived"):
		intent = "delivery_status"
	}
	urgency := "normal"
	for _, k := range []string{"today", "urgent", "asap", "immediately", "right now"} {
		if strings.Contains(low, k) {
			urgency = "high"
		}
	}
	obj := fmt.Sprintf(`{"order_id": %q, "intent": %q, "urgency": %q}`, order, intent, urgency)
	if every(t.jsonExtraKeyEvery, h) {
		obj = fmt.Sprintf(`{"order_id": %q, "intent": %q, "urgency": %q, "confidence": 0.93}`, order, intent, urgency)
	}
	if every(t.jsonFenceEvery, h) {
		return "```json\n" + obj + "\n```"
	}
	return obj
}

func rewriteQuery(low string) string {
	var terms []string
	for _, k := range keywords(low) {
		switch k {
		case "cheap", "cheapest", "express", "fast", "kg", "under", "need", "want", "please", "send", "ship", "shipping", "parcel":
			continue
		}
		if _, err := fmt.Sscanf(k, "%d", new(int)); err == nil {
			continue
		}
		terms = append(terms, k)
	}
	filters := []string{}
	if m := weightRe.FindStringSubmatch(low); m != nil {
		filters = append(filters, "max_weight:"+m[1]+"kg")
	}
	if strings.Contains(low, "cheap") {
		filters = append(filters, "sort:price_asc")
	}
	if strings.Contains(low, "express") || strings.Contains(low, "fast") {
		filters = append(filters, "service:express")
	}
	out, _ := json.Marshal(map[string]any{"query": strings.Join(terms, " "), "filters": filters})
	return string(out)
}

func orderAssistant(t traits, user string, last chatMessage, h int) reply {
	if last.Role == "tool" {
		var res map[string]any
		_ = json.Unmarshal([]byte(last.text()), &res)
		status, _ := res["status"].(string)
		if status == "" {
			status = "being processed"
		}
		return reply{content: styled(t, fmt.Sprintf("Your order is %s. You can follow each scan on the tracking page.", status))}
	}
	call := func(name, args string) reply {
		tc := toolCall{ID: fmt.Sprintf("call_%x", h), Type: "function"}
		tc.Function.Name, tc.Function.Arguments = name, args
		return reply{toolCalls: []toolCall{tc}}
	}
	if tn := trackingRe.FindString(user); tn != "" {
		return call("track_parcel", fmt.Sprintf(`{"tracking_number": %q}`, tn))
	}
	if id := orderRe.FindString(user); id != "" {
		if every(t.toolSkipEvery, h) {
			return reply{content: "I'd be happy to help with order " + id + ". Could you confirm the email address on the order first?"}
		}
		if every(t.toolIntArgEvery, h) {
			return call("lookup_order", fmt.Sprintf(`{"order_id": %s}`, id))
		}
		return call("lookup_order", fmt.Sprintf(`{"order_id": %q}`, id))
	}
	return reply{content: "Could you share your order number or tracking number so I can look it up?"}
}
