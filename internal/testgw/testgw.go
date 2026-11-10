// Package testgw is an in-memory double of the Bifrost endpoints Heimdall
// uses, for hermetic tests. It forwards inference to the simulator, applies
// routing rules with weights and fallbacks, and keeps a log store that
// answers the same queries the real gateway does.
//
// It is a test double only. The opt-in suite in ./e2e runs the same flows
// against a real Bifrost binary; this package exists so that `go test ./...`
// needs nothing but Go.
package testgw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
)

type logEntry struct {
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
	Latency         float64                    `json:"latency"`
	Cost            float64                    `json:"cost"`
	Status          string                     `json:"status"`
	StopReason      string                     `json:"stop_reason"`
	Metadata        map[string]any             `json:"metadata"`
	InputHistory    json.RawMessage            `json:"input_history"`
	OutputMessage   json.RawMessage            `json:"output_message,omitempty"`
	Params          map[string]json.RawMessage `json:"params"`
	TokenUsage      map[string]int             `json:"token_usage,omitempty"`
}

type rule struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Enabled       bool     `json:"enabled"`
	CELExpression string   `json:"cel_expression"`
	Targets       []target `json:"targets"`
	Fallbacks     []string `json:"fallbacks"`
	Scope         string   `json:"scope"`
	Priority      int      `json:"priority"`
	hits          int
}

type target struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Weight   float64 `json:"weight"`
}

// Gateway is the in-memory gateway double.
type Gateway struct {
	World     *sim.World
	AdminUser string
	AdminPass string
	Server    *httptest.Server

	mu     sync.Mutex
	logs   []*logEntry
	rules  []*rule
	vks    map[string][2]string // value -> id, name
	seq    int
	simSrv *httptest.Server
}

// New starts a gateway double in front of a fresh simulated world.
func New(now time.Time) *Gateway {
	w := sim.NewWorld(now)
	w.Fast = true
	g := &Gateway{World: w, AdminUser: "admin", AdminPass: "secret", vks: map[string][2]string{sim.HeimdallKey: {"vk-heimdall", "heimdall"}}}
	for _, c := range sim.Consumers {
		g.vks[c.Value] = [2]string{c.ID, c.Name}
	}
	g.simSrv = httptest.NewServer(w.Handler())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(rw http.ResponseWriter, _ *http.Request) { write(rw, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/chat/completions", g.chat)
	mux.HandleFunc("GET /v1/models", g.models)
	mux.HandleFunc("GET /v1/models/{provider}/{model}", g.model)
	mux.HandleFunc("POST /relay/tavily/{op}", g.relay)
	mux.HandleFunc("GET /api/logs", g.admin(g.listLogs))
	mux.HandleFunc("GET /api/logs/rankings", g.admin(g.rankings))
	mux.HandleFunc("GET /api/logs/rankings/by-dimension", g.admin(g.rankingsBy))
	mux.HandleFunc("GET /api/logs/{id}", g.admin(g.logDetail))
	mux.HandleFunc("GET /api/routing/rules", g.admin(g.listRules))
	mux.HandleFunc("POST /api/routing/rules", g.admin(g.createRule))
	mux.HandleFunc("PUT /api/routing/rules/{id}", g.admin(g.updateRule))
	mux.HandleFunc("DELETE /api/routing/rules/{id}", g.admin(g.deleteRule))
	g.Server = httptest.NewServer(mux)
	return g
}

// Close stops the gateway and the simulator behind it.
func (g *Gateway) Close() {
	g.Server.Close()
	g.simSrv.Close()
}

// URL is the gateway's base URL.
func (g *Gateway) URL() string { return g.Server.URL }

// RuleCount returns the number of routing rules.
func (g *Gateway) RuleCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.rules)
}

func write(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}

func fail(rw http.ResponseWriter, status int, typ, msg string) {
	write(rw, status, map[string]any{"type": typ, "status_code": status, "error": map[string]string{"message": msg}})
}

func (g *Gateway) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != g.AdminUser || p != g.AdminPass {
			fail(rw, 401, "unauthorized", "admin credentials required")
			return
		}
		next(rw, r)
	}
}

func (g *Gateway) vk(r *http.Request) ([2]string, bool) {
	v, ok := g.vks[r.Header.Get("x-bf-vk")]
	return v, ok
}

// upstream sends a request to the simulator.
func (g *Gateway) upstream(method, path string, body []byte) (int, []byte) {
	req, _ := http.NewRequest(method, g.simSrv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.World.Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 502, []byte(`{"error":{"message":"upstream unreachable"}}`)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func split(id string) (string, string) {
	p, m, _ := strings.Cut(id, "/")
	return p, m
}

// route applies the first enabled rule whose expression names this model.
func (g *Gateway) route(provider, model string) (primary [2]string, fallbacks []string, matched *rule) {
	primary = [2]string{provider, model}
	g.mu.Lock()
	defer g.mu.Unlock()
	rules := append([]*rule{}, g.rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	want := fmt.Sprintf("provider == %q && model == %q", provider, model)
	for _, r := range rules {
		if !r.Enabled || r.CELExpression != want || len(r.Targets) == 0 {
			continue
		}
		// deterministic weighted pick: spread hits across targets by weight
		r.hits++
		point := float64((r.hits*37)%100) / 100
		acc := 0.0
		pick := r.Targets[len(r.Targets)-1]
		for _, t := range r.Targets {
			acc += t.Weight
			if point < acc {
				pick = t
				break
			}
		}
		return [2]string{pick.Provider, pick.Model}, r.Fallbacks, r
	}
	return primary, nil, nil
}

func (g *Gateway) chat(rw http.ResponseWriter, r *http.Request) {
	vk, ok := g.vk(r)
	if !ok {
		fail(rw, 401, "virtual_key_required", "virtual key is required")
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		fail(rw, 400, "invalid_request", "invalid JSON")
		return
	}
	var modelID string
	_ = json.Unmarshal(body["model"], &modelID)
	provider, model := split(modelID)
	var reqFallbacks []string
	_ = json.Unmarshal(body["fallbacks"], &reqFallbacks)

	primary, ruleFallbacks, matched := g.route(provider, model)
	attempts := [][2]string{primary}
	for _, fb := range append(ruleFallbacks, reqFallbacks...) {
		p, m := split(fb)
		attempts = append(attempts, [2]string{p, m})
	}

	meta := map[string]any{}
	for k, v := range r.Header {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-bf-lh-") && len(v) > 0 {
			meta[strings.TrimPrefix(lk, "x-bf-lh-")] = v[0]
		}
	}
	params := map[string]json.RawMessage{}
	for k, v := range body {
		if k != "model" && k != "messages" && k != "fallbacks" {
			params[k] = v
		}
	}

	var parentID *string
	for i, at := range attempts {
		up := map[string]json.RawMessage{}
		for k, v := range body {
			up[k] = v
		}
		delete(up, "fallbacks")
		mb, _ := json.Marshal(at[1])
		up["model"] = mb
		payload, _ := json.Marshal(up)
		start := time.Now()
		status, data := g.upstream(http.MethodPost, "/"+at[0]+"/v1/chat/completions", payload)
		entry := &logEntry{Timestamp: time.Now().UTC(), Object: "chat_completion", Provider: at[0], Model: at[1], FallbackIndex: i,
			ParentRequestID: parentID, Latency: float64(time.Since(start).Microseconds())/1000 + 1, Metadata: meta,
			InputHistory: body["messages"], Params: params, VirtualKeyID: &vk[0], VirtualKeyName: &vk[1]}
		if len(meta) == 0 {
			entry.Metadata = nil
		}
		if matched != nil {
			entry.RoutingRuleID, entry.RoutingRuleName = &matched.ID, &matched.Name
		}
		g.mu.Lock()
		g.seq++
		entry.ID = fmt.Sprintf("log-%06d", g.seq)
		g.logs = append(g.logs, entry)
		g.mu.Unlock()
		if i == 0 {
			id := entry.ID
			parentID = &id
		}
		if status != 200 {
			entry.Status = "error"
			if i == len(attempts)-1 {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(status)
				_, _ = rw.Write(data)
				return
			}
			continue
		}
		var resp struct {
			Choices []struct {
				FinishReason string          `json:"finish_reason"`
				Message      json.RawMessage `json:"message"`
			} `json:"choices"`
			Usage map[string]int `json:"usage"`
		}
		_ = json.Unmarshal(data, &resp)
		entry.Status = "success"
		if len(resp.Choices) > 0 {
			entry.OutputMessage, entry.StopReason = resp.Choices[0].Message, resp.Choices[0].FinishReason
		}
		entry.TokenUsage = resp.Usage
		if p, ok := sim.Pricing()[at[0]+"/"+at[1]]; ok {
			entry.Cost = float64(resp.Usage["prompt_tokens"])*p["input_cost_per_token"].(float64) + float64(resp.Usage["completion_tokens"])*p["output_cost_per_token"].(float64)
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		out["extra_fields"] = map[string]any{"provider": at[0], "routing_info": map[string]any{"provider": at[0], "model": at[1], "is_fallback": i > 0}}
		write(rw, 200, out)
		return
	}
}

func (g *Gateway) models(rw http.ResponseWriter, r *http.Request) {
	if _, ok := g.vk(r); !ok {
		fail(rw, 401, "virtual_key_required", "virtual key is required")
		return
	}
	data := []map[string]any{}
	for _, id := range sim.ModelIDs() {
		p := sim.Pricing()[id]
		data = append(data, map[string]any{"id": id, "created": 1, "owned_by": strings.Split(id, "/")[0],
			"pricing": map[string]string{"prompt": strconv.FormatFloat(p["input_cost_per_token"].(float64), 'f', 10, 64),
				"completion": strconv.FormatFloat(p["output_cost_per_token"].(float64), 'f', 10, 64)}})
	}
	write(rw, 200, map[string]any{"data": data})
}

func (g *Gateway) model(rw http.ResponseWriter, r *http.Request) {
	if _, ok := g.vk(r); !ok {
		fail(rw, 401, "virtual_key_required", "virtual key is required")
		return
	}
	status, data := g.upstream(http.MethodGet, "/"+r.PathValue("provider")+"/v1/models/"+r.PathValue("model"), nil)
	if status != 200 {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(status)
		_, _ = rw.Write(data)
		return
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	out["id"] = r.PathValue("provider") + "/" + r.PathValue("model")
	write(rw, 200, out)
}

func (g *Gateway) relay(rw http.ResponseWriter, r *http.Request) {
	vk, ok := g.vk(r)
	if !ok || vk[1] != "heimdall" {
		fail(rw, 403, "relay_blocked", "Relay 'tavily' is not allowed for this virtual key")
		return
	}
	body, _ := io.ReadAll(r.Body)
	status, data := g.upstream(http.MethodPost, "/tavily/"+r.PathValue("op"), body)
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_, _ = rw.Write(data)
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

func csv(q string) map[string]bool {
	set := map[string]bool{}
	for _, p := range strings.Split(q, ",") {
		if p = strings.TrimSpace(p); p != "" {
			set[p] = true
		}
	}
	return set
}

func (g *Gateway) filtered(r *http.Request) []*logEntry {
	q := r.URL.Query()
	providers, models, status, ruleIDs := csv(q.Get("providers")), csv(q.Get("models")), csv(q.Get("status")), csv(q.Get("routing_rule_ids"))
	start, _ := time.Parse(time.RFC3339, q.Get("start_time"))
	end, _ := time.Parse(time.RFC3339, q.Get("end_time"))
	metaWant := map[string]string{}
	for k, v := range q {
		if strings.HasPrefix(k, "metadata_") && len(v) > 0 {
			metaWant[strings.TrimPrefix(k, "metadata_")] = v[0]
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []*logEntry
	for _, l := range g.logs {
		switch {
		case len(providers) > 0 && !providers[l.Provider],
			len(models) > 0 && !models[l.Model],
			len(status) > 0 && !status[l.Status],
			len(ruleIDs) > 0 && (l.RoutingRuleID == nil || !ruleIDs[*l.RoutingRuleID]),
			!start.IsZero() && l.Timestamp.Before(start),
			!end.IsZero() && l.Timestamp.After(end.Add(time.Second)):
			continue
		}
		ok := true
		for k, v := range metaWant {
			if fmt.Sprint(l.Metadata[k]) != v {
				ok = false
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

func (g *Gateway) listLogs(rw http.ResponseWriter, r *http.Request) {
	all := g.filtered(r)
	q := r.URL.Query()
	if q.Get("order") != "asc" {
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	total := len(all)
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	if len(all) > limit {
		all = all[:limit]
	}
	// the list view carries a shortened conversation, like the real gateway
	page := make([]logEntry, len(all))
	for i, l := range all {
		page[i] = *l
		page[i].Params = nil
	}
	write(rw, 200, map[string]any{"logs": page, "pagination": map[string]int{"total_count": total}, "stats": map[string]any{"total_requests": total}})
}

func (g *Gateway) logDetail(rw http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, l := range g.logs {
		if l.ID == r.PathValue("id") {
			write(rw, 200, l)
			return
		}
	}
	fail(rw, 404, "not_found", "log not found")
}

func (g *Gateway) rankings(rw http.ResponseWriter, r *http.Request) {
	type agg struct {
		Model         string  `json:"model"`
		Provider      string  `json:"provider"`
		TotalRequests int     `json:"total_requests"`
		SuccessCount  int     `json:"success_count"`
		SuccessRate   float64 `json:"success_rate"`
		TotalTokens   int     `json:"total_tokens"`
		TotalCost     float64 `json:"total_cost"`
		AvgLatency    float64 `json:"avg_latency"`
	}
	m := map[string]*agg{}
	var keys []string
	for _, l := range g.filtered(r) {
		k := l.Provider + "/" + l.Model
		a := m[k]
		if a == nil {
			a = &agg{Model: l.Model, Provider: l.Provider}
			m[k] = a
			keys = append(keys, k)
		}
		a.TotalRequests++
		if l.Status == "success" {
			a.SuccessCount++
		}
		a.TotalTokens += l.TokenUsage["total_tokens"]
		a.TotalCost += l.Cost
		a.AvgLatency += l.Latency
	}
	sort.Strings(keys)
	out := []agg{}
	for _, k := range keys {
		a := m[k]
		a.AvgLatency /= float64(a.TotalRequests)
		a.SuccessRate = 100 * float64(a.SuccessCount) / float64(a.TotalRequests)
		out = append(out, *a)
	}
	write(rw, 200, map[string]any{"rankings": out})
}

func (g *Gateway) rankingsBy(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("dimension") != "virtual_key" {
		fail(rw, 400, "bad_request", "only the virtual_key dimension is implemented in the test double")
		return
	}
	type agg struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		TotalRequests int     `json:"total_requests"`
		TotalCost     float64 `json:"total_cost"`
	}
	m := map[string]*agg{}
	var keys []string
	for _, l := range g.filtered(r) {
		if l.VirtualKeyID == nil {
			continue
		}
		a := m[*l.VirtualKeyID]
		if a == nil {
			a = &agg{ID: *l.VirtualKeyID, Name: *l.VirtualKeyName}
			m[a.ID] = a
			keys = append(keys, a.ID)
		}
		a.TotalRequests++
		a.TotalCost += l.Cost
	}
	sort.Strings(keys)
	out := []agg{}
	for _, k := range keys {
		out = append(out, *m[k])
	}
	write(rw, 200, map[string]any{"rankings": out, "dimension": "virtual_key"})
}

// ---------------------------------------------------------------------------
// Routing rules
// ---------------------------------------------------------------------------

func (g *Gateway) listRules(rw http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []rule{}
	for _, r := range g.rules {
		out = append(out, *r)
	}
	write(rw, 200, map[string]any{"rules": out, "count": len(out)})
}

func validRule(r *rule) string {
	sum := 0.0
	for _, t := range r.Targets {
		sum += t.Weight
	}
	if len(r.Targets) == 0 || sum < 0.999 || sum > 1.001 {
		return "target weights must sum to 1"
	}
	return ""
}

func (g *Gateway) createRule(rw http.ResponseWriter, r *http.Request) {
	var in rule
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		fail(rw, 400, "bad_request", "invalid JSON")
		return
	}
	if msg := validRule(&in); msg != "" {
		fail(rw, 400, "bad_request", msg)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.rules {
		if e.Priority == in.Priority && e.Scope == in.Scope {
			fail(rw, 400, "bad_request", "priority must be unique within the scope")
			return
		}
	}
	g.seq++
	in.ID = fmt.Sprintf("rule-%04d", g.seq)
	g.rules = append(g.rules, &in)
	write(rw, 201, map[string]any{"message": "Routing rule created successfully", "rule": in})
}

func (g *Gateway) updateRule(rw http.ResponseWriter, r *http.Request) {
	var in rule
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		fail(rw, 400, "bad_request", "invalid JSON")
		return
	}
	if msg := validRule(&in); msg != "" {
		fail(rw, 400, "bad_request", msg)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.rules {
		if e.ID == r.PathValue("id") {
			in.ID = e.ID
			*e = in
			write(rw, 200, map[string]any{"message": "Routing rule updated successfully", "rule": in})
			return
		}
	}
	fail(rw, 404, "not_found", "rule not found")
}

func (g *Gateway) deleteRule(rw http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, e := range g.rules {
		if e.ID == r.PathValue("id") {
			g.rules = append(g.rules[:i], g.rules[i+1:]...)
			write(rw, 200, map[string]string{"message": "Routing rule deleted successfully"})
			return
		}
	}
	fail(rw, 404, "not_found", "rule not found")
}
