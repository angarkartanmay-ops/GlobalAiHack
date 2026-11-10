package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/mcpserver"
	"github.com/angarkartanmay-ops/globalaihack/internal/rehearsal"
	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
	"github.com/angarkartanmay-ops/globalaihack/internal/store"
	"github.com/angarkartanmay-ops/globalaihack/internal/testgw"
)

// harness is Heimdall wired to the in-memory gateway double, with the
// simulated applications' traffic already sent.
type harness struct {
	app *App
	gw  *testgw.Gateway
	api *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	gw := testgw.New(time.Now())
	cfg := Config{Mode: ModeSim, DataDir: t.TempDir(), BifrostURL: gw.URL(), AdminUser: gw.AdminUser, AdminPass: gw.AdminPass,
		VirtualKey: sim.HeimdallKey, AnalystModel: sim.AnalystModel, JudgeModel: sim.AnalystModel, TavilyRelay: "tavily", OpenWeb: true,
		Lookback: 24 * time.Hour, RehearsalSamples: 40, RehearsalBudget: 1, SimKey: sim.DefaultKey}
	for i, c := range sim.Consumers {
		cfg.TrafficKeys[i] = c.Value
	}
	app, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var st sim.Stats
	sim.Send(context.Background(), app.GW, sim.DefaultWorkload(), 8, &st)
	if st.Sent.Load() < 100 {
		t.Fatalf("workload sent only %d requests", st.Sent.Load())
	}
	h := &harness{app: app, gw: gw, api: httptest.NewServer(app.Handler(nil, mcpserver.New(app)))}
	t.Cleanup(func() { h.api.Close(); app.Close(); gw.Close() })
	return h
}

func (h *harness) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.api.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (h *harness) scan(t *testing.T) map[string]core.Exposure {
	t.Helper()
	if _, err := h.app.RunScan(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	exposures, _ := h.app.Exposures(context.Background())
	out := map[string]core.Exposure{}
	for _, e := range exposures {
		out[e.ID()] = e
	}
	return out
}

func TestScanJoinsNoticesWithTraffic(t *testing.T) {
	h := newHarness(t)
	ex := h.scan(t)
	w := h.gw.World

	large := ex["aurora/aurora-large-1"]
	if large.Finding == nil || large.Finding.Status != core.StatusDeprecated || large.Finding.ShutdownDate != w.LargeShutdown().Format("2006-01-02") {
		t.Fatalf("aurora-large-1: %+v", large.Finding)
	}
	if large.DaysLeft == nil || *large.DaysLeft != 23 || large.Risk != core.RiskHigh || large.Finding.Confidence != core.ConfidenceHigh {
		t.Errorf("aurora-large-1: days=%v risk=%s confidence=%s", large.DaysLeft, large.Risk, large.Finding.Confidence)
	}
	if large.Finding.Replacement != "aurora/aurora-large-2" || !large.Finding.ReplacementOnGW {
		t.Errorf("replacement = %q", large.Finding.Replacement)
	}
	if large.Finding.Conflict == nil {
		t.Error("the third-party date that disagrees with the provider's should be reported as a conflict")
	}
	if large.Traffic == nil || large.Traffic.RequestShare < 0.5 || len(large.Traffic.Consumers) != 2 {
		t.Errorf("aurora-large-1 traffic: %+v", large.Traffic)
	}

	mini := ex["aurora/aurora-mini-1"].Finding
	var gotGateway bool
	for _, e := range mini.Evidence {
		gotGateway = gotGateway || e.Kind == "gateway"
		if !e.Grounded {
			t.Errorf("ungrounded evidence kept: %+v", e)
		}
	}
	if !gotGateway || mini.Discarded != 1 || mini.Confidence != core.ConfidenceHigh {
		t.Errorf("aurora-mini-1 should have the gateway's shutdown_date as evidence and one discarded misquote: gateway=%v discarded=%d confidence=%s",
			gotGateway, mini.Discarded, mini.Confidence)
	}

	if e := ex["aurora/aurora-instant-0"]; e.Finding.Status != core.StatusRetired || e.Risk != core.RiskCritical {
		t.Errorf("a retired model still receiving requests should be critical: %s %s", e.Finding.Status, e.Risk)
	}
	if f := ex["lumen/lumen-chat-2-0815"].Finding; f.Status != core.StatusDeprecated || f.DatePrecision != "month" || f.Confidence != core.ConfidenceMedium {
		t.Errorf("lumen-chat-2-0815: %s precision=%s confidence=%s", f.Status, f.DatePrecision, f.Confidence)
	}
	if e := ex["lumen/lumen-chat-3"]; e.Finding.Status != core.StatusActive || e.Risk != core.RiskNone {
		t.Errorf("lumen-chat-3 should be active with no risk: %s %s", e.Finding.Status, e.Risk)
	}

	// the most urgent exposure comes first
	ordered, _ := h.app.Exposures(context.Background())
	if ordered[0].ID() != "aurora/aurora-instant-0" || ordered[1].ID() != "aurora/aurora-large-1" {
		t.Errorf("order = %s, %s", ordered[0].ID(), ordered[1].ID())
	}
	evs, _ := store.List[core.Event](h.app.Store, store.Events, true, 0)
	if len(evs) != 4 {
		t.Errorf("expected one event per at-risk model, got %d", len(evs))
	}
}

func TestHeimdallsOwnTrafficIsNotDemand(t *testing.T) {
	h := newHarness(t)
	h.scan(t) // analyst calls, model lookups and relay calls all go through the gateway
	reh := h.app.runner.Run(context.Background(), rehearsal.Options{ID: "r1", Baseline: "aurora/aurora-large-1", Candidate: "aurora/aurora-large-2", Samples: 20}, nil)
	if reh.Status != "done" {
		t.Fatalf("rehearsal: %s %s", reh.Status, reh.Error)
	}
	inv, err := h.app.RefreshInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range inv.Items {
		if it.ID() == sim.AnalystModel || it.ID() == "aurora/aurora-large-2" {
			t.Errorf("%s appears in the inventory, but only Heimdall called it", it.ID())
		}
		for _, c := range it.Consumers {
			if c.Name == "heimdall" {
				t.Errorf("heimdall listed as a consumer of %s", it.ID())
			}
		}
	}
}

func TestRehearsalSeparatesADropInFromARewrite(t *testing.T) {
	h := newHarness(t)
	run := func(candidate string) core.Rehearsal {
		r := h.app.runner.Run(context.Background(), rehearsal.Options{ID: "r-" + candidate, Baseline: "aurora/aurora-large-1", Candidate: candidate, Samples: 200}, nil)
		if r.Status != "done" {
			t.Fatalf("%s: %s %s", candidate, r.Status, r.Error)
		}
		return r
	}
	good := run("aurora/aurora-large-2")
	if good.Verdict != core.ReadyCaveats || good.Score < 90 || good.CheckFailures[rehearsal.CheckJSON] != 4 || good.CheckFailures[rehearsal.CheckToolCalls] != 2 {
		t.Errorf("aurora-large-2: verdict=%s score=%.1f failures=%v", good.Verdict, good.Score, good.CheckFailures)
	}
	if good.FresherCount == 0 || good.StalerCount != 0 || good.CostDeltaPct >= 0 {
		t.Errorf("aurora-large-2 should be fresher on live evidence and cheaper: fresher=%d staler=%d cost=%.1f%%", good.FresherCount, good.StalerCount, good.CostDeltaPct)
	}
	bad := run("lumen/lumen-chat-3")
	if bad.Verdict != core.ReadyNo || bad.CheckFailures[rehearsal.CheckToolCalls] == 0 || bad.CheckFailures[rehearsal.CheckRefusal] == 0 || bad.StalerCount == 0 {
		t.Errorf("lumen-chat-3: verdict=%s score=%.1f failures=%v staler=%d", bad.Verdict, bad.Score, bad.CheckFailures, bad.StalerCount)
	}
	// every tiebreak that names a winner rests on quoted evidence
	for _, s := range append(good.Samples, bad.Samples...) {
		for _, tb := range s.Tiebreaks {
			if tb.Winner != "neither" && len(tb.Evidence) == 0 {
				t.Errorf("tiebreak without evidence: %+v", tb)
			}
		}
	}
	if r := h.app.runner.Run(context.Background(), rehearsal.Options{ID: "none", Baseline: "aurora/aurora-mini-2", Candidate: "aurora/aurora-large-2"}, nil); r.Status != "failed" {
		t.Error("a baseline with no traffic cannot be rehearsed")
	}
}

func (h *harness) ask(t *testing.T, model string) (string, bool) {
	t.Helper()
	msgs, _ := json.Marshal([]map[string]string{{"role": "system", "content": sim.SysSupport}, {"role": "user", "content": "What is the weekend delivery surcharge?"}})
	resp, err := h.app.GW.ChatAs(context.Background(), bifrost.ChatRequest{Model: model, Messages: msgs}, sim.Consumers[1].Value, nil)
	if err != nil {
		return err.Error(), false
	}
	return resp.Model, true
}

func TestMigrationLifecycleOnTheGateway(t *testing.T) {
	h := newHarness(t)
	const old, repl = "aurora/aurora-large-1", "aurora/aurora-large-2"
	var mig core.Migration

	// Without a rule, a provider-side shutdown reaches the application.
	h.gw.World.Retire("aurora-large-1")
	if got, ok := h.ask(t, old); ok {
		t.Fatalf("expected the retired model to fail, got %s", got)
	}
	h.gw.World.Revive("aurora-large-1")

	if code := h.do(t, "POST", "/api/migrations", map[string]any{"model": old, "replacement": repl, "stage": "armed"}, &mig); code != 200 || mig.RuleID == "" {
		t.Fatalf("arm: %d %+v", code, mig)
	}
	h.gw.World.Retire("aurora-large-1")
	if got, ok := h.ask(t, old); !ok || got != "aurora-large-2" {
		t.Fatalf("armed safety net should serve from the replacement, got %s ok=%v", got, ok)
	}
	h.gw.World.Revive("aurora-large-1")

	if code := h.do(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "canary", "canary_pct": 30}, &mig); code != 200 || mig.CanaryPct != 30 {
		t.Fatalf("canary: %d %+v", code, mig)
	}
	served := map[string]int{}
	for i := 0; i < 100; i++ {
		got, ok := h.ask(t, old)
		if !ok {
			t.Fatal(got)
		}
		served[got]++
	}
	if served["aurora-large-2"] < 20 || served["aurora-large-2"] > 40 {
		t.Errorf("canary split = %v, want about 30 on the replacement", served)
	}
	health, err := h.app.cut.Health(context.Background(), mig)
	if err != nil || health.NewRequests != served["aurora-large-2"] || health.OldRequests != served["aurora-large-1"] || health.NewSuccess != 100 {
		t.Errorf("health = %+v, %v", health, err)
	}

	if code := h.do(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "promoted"}, &mig); code != 200 {
		t.Fatalf("promote: %d", code)
	}
	if got, _ := h.ask(t, old); got != "aurora-large-2" {
		t.Errorf("promoted: served by %s", got)
	}
	if h.gw.RuleCount() != 1 {
		t.Errorf("one migration should be exactly one rule, found %d", h.gw.RuleCount())
	}

	if code := h.do(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "none"}, &mig); code != 200 || mig.Stage != core.StageNone {
		t.Fatalf("rollback: %d %+v", code, mig)
	}
	if h.gw.RuleCount() != 0 {
		t.Errorf("rollback should leave no rule behind, found %d", h.gw.RuleCount())
	}
	if got, _ := h.ask(t, old); got != "aurora-large-1" {
		t.Errorf("after rollback: served by %s", got)
	}
	if len(mig.History) != 4 {
		t.Errorf("history = %+v", mig.History)
	}
	if code := h.do(t, "POST", "/api/migrations", map[string]any{"model": old, "replacement": old, "stage": "armed"}, nil); code == 200 {
		t.Error("a model cannot replace itself")
	}
}

func TestFreshnessEndpoint(t *testing.T) {
	h := newHarness(t)
	var rep core.FreshnessReport
	code := h.do(t, "POST", "/api/freshness", map[string]any{"rewrite": true,
		"question": "Which API version should I use, and is there a weekend fee?",
		"answer":   "New integrations should use Northpost API v4, which is the current version. Weekend delivery carries a surcharge of $4.50 per parcel. Standard delivery takes 2 to 4 business days. Our warehouse uses blue boxes."}, &rep)
	if code != 200 {
		t.Fatalf("status %d: %s", code, rep.Error)
	}
	if rep.Changed != 2 || rep.Supported != 1 || rep.NotChecked != 1 || rep.Unverified != 0 {
		t.Fatalf("changed=%d supported=%d unverifiable=%d not_checked=%d", rep.Changed, rep.Supported, rep.Unverified, rep.NotChecked)
	}
	for _, c := range rep.Claims {
		if c.Checked && c.Verdict != core.VerdictUnverifiable && (len(c.Evidence) == 0 || !c.Evidence[0].Grounded || c.Evidence[0].URL == "") {
			t.Errorf("claim %q has a verdict without grounded evidence", c.Text)
		}
	}
	if rep.Claims[0].CurrentValue != "v5" || rep.Claims[0].Confidence != core.ConfidenceHigh {
		t.Errorf("first claim: now=%q confidence=%s (two independent domains state v5)", rep.Claims[0].CurrentValue, rep.Claims[0].Confidence)
	}
	if !strings.Contains(rep.Rewrite, "v5 [1]") || !strings.Contains(rep.Rewrite, "Sources:") {
		t.Errorf("rewrite = %q", rep.Rewrite)
	}
	if code := h.do(t, "POST", "/api/freshness", map[string]any{"answer": "  "}, nil); code != 400 {
		t.Errorf("empty answer: status %d", code)
	}
}

func TestMCPTools(t *testing.T) {
	h := newHarness(t)
	h.scan(t)
	call := func(method string, params any) map[string]any {
		var out map[string]any
		h.do(t, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, &out)
		res, _ := out["result"].(map[string]any)
		return res
	}
	if v := call("initialize", map[string]any{"protocolVersion": "2025-03-26"}); v["serverInfo"] == nil {
		t.Fatalf("initialize: %v", v)
	}
	if tools := call("tools/list", nil)["tools"].([]any); len(tools) != 3 {
		t.Fatalf("tools = %d", len(tools))
	}
	text := func(res map[string]any) string {
		return res["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	if got := text(call("tools/call", map[string]any{"name": "heimdall_list_sunsets", "arguments": map[string]any{}})); !strings.Contains(got, "aurora/aurora-large-1: deprecated") || !strings.Contains(got, "checkout-app") {
		t.Errorf("list_sunsets: %s", got)
	}
	if got := text(call("tools/call", map[string]any{"name": "heimdall_check_answer", "arguments": map[string]any{"answer": "The Chief Executive Officer of Northpost is Daniel Okafor."}})); !strings.Contains(got, "[CHANGED]") || !strings.Contains(got, "Priya Raman") {
		t.Errorf("check_answer: %s", got)
	}
	if res := call("tools/call", map[string]any{"name": "heimdall_check_model", "arguments": map[string]any{"model": "no-slash"}}); res["isError"] != true {
		t.Errorf("bad input should be a tool error: %v", res)
	}
	// notifications get no response body
	req, _ := http.NewRequest("POST", h.api.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d", resp.StatusCode)
	}
}

func TestLiveModeIsLockedWithoutAToken(t *testing.T) {
	mk := func(cfg Config) *httptest.Server {
		cfg.DataDir = t.TempDir()
		cfg.BifrostURL = "http://127.0.0.1:1"
		app, err := New(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(app.Handler(nil, nil))
		t.Cleanup(func() { srv.Close(); app.Close() })
		return srv
	}
	get := func(srv *httptest.Server, path, token string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	locked := mk(Config{Mode: ModeLive})
	if code := get(locked, "/api/events", ""); code != http.StatusForbidden {
		t.Errorf("live mode with no token should refuse everyone, got %d", code)
	}
	if code := get(locked, "/api/status", ""); code != 200 {
		t.Errorf("status should stay readable so the UI can explain the lock, got %d", code)
	}
	guarded := mk(Config{Mode: ModeLive, AdminToken: "s3cret"})
	if code := get(guarded, "/api/events", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code := get(guarded, "/api/events", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code := get(guarded, "/api/events", "s3cret"); code != 200 {
		t.Errorf("right token: %d", code)
	}
	req, _ := http.NewRequest("GET", guarded.URL+"/sim/aurora/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+sim.DefaultKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "aurora-large-1") {
		t.Error("the simulator must not be mounted in live mode")
	}
}

func TestBootstrap(t *testing.T) {
	dir := t.TempDir()
	noEnv := func(string) string { return "" }
	res, err := Bootstrap(BootstrapOptions{Dir: dir, HeimdallURL: "http://127.0.0.1:9000", Getenv: noEnv})
	if err != nil || res.Mode != ModeSim {
		t.Fatalf("no keys should mean simulator mode: %+v %v", res, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	cfg := string(raw)
	for _, want := range []string{`"base_url": "http://127.0.0.1:9000/sim/aurora"`, `"base_url": "http://127.0.0.1:9000/sim/tavily"`, `"relay_name": "tavily"`, `"enforce_auth_on_inference": true`, `"admin_password": "env.BIFROST_ADMIN_PASSWORD"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("simulator config is missing %s", want)
		}
	}
	if res.Env["BIFROST_VIRTUAL_KEY"] != sim.HeimdallKey || res.Env["HEIMDALL_TAVILY_RELAY"] != "tavily" {
		t.Errorf("env = %v", res.Env)
	}

	env := map[string]string{"OPENAI_API_KEY": "sk-real", "TAVILY_API_KEY": "tvly-real"}
	res, err = Bootstrap(BootstrapOptions{Dir: dir, Getenv: func(k string) string { return env[k] }})
	if err != nil || res.Mode != ModeLive || len(res.Providers) != 1 || !res.Tavily {
		t.Fatalf("a provider key should mean live mode: %+v %v", res, err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "config.json"))
	cfg = string(raw)
	if strings.Contains(cfg, "sk-real") || strings.Contains(cfg, "tvly-real") || strings.Contains(cfg, res.Env["BIFROST_ADMIN_PASSWORD"]) {
		t.Error("secrets must be referenced by environment variable, never written into config.json")
	}
	for _, want := range []string{`"value": "env.OPENAI_API_KEY"`, `"base_url": "https://api.tavily.com"`, `"value": "env.TAVILY_API_KEY"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("live config is missing %s", want)
		}
	}
	if strings.Contains(cfg, "/sim/") || res.Env["HEIMDALL_ADMIN_TOKEN"] == "" {
		t.Error("live config must not point at the simulator, and a dashboard token must be generated")
	}
	// secrets are stable across restarts
	again, _ := Bootstrap(BootstrapOptions{Dir: dir, Getenv: func(k string) string { return env[k] }})
	if again.Env["BIFROST_VIRTUAL_KEY"] != res.Env["BIFROST_VIRTUAL_KEY"] || again.Env["BIFROST_ADMIN_PASSWORD"] != res.Env["BIFROST_ADMIN_PASSWORD"] {
		t.Error("generated secrets should persist between runs")
	}
	if _, err := Bootstrap(BootstrapOptions{Dir: dir, Mode: ModeLive, Getenv: noEnv}); err == nil {
		t.Error("forcing live mode without a provider key should fail")
	}
}

func TestPickAnalyst(t *testing.T) {
	mk := func(ids ...string) []bifrost.ModelInfo {
		var out []bifrost.ModelInfo
		for i, id := range ids {
			out = append(out, bifrost.ModelInfo{ID: id, Created: int64(i)})
		}
		return out
	}
	cases := []struct {
		models []bifrost.ModelInfo
		want   string
	}{
		{mk("openai/gpt-4o", "openai/gpt-4o-mini", "openai/text-embedding-3-small", "openai/gpt-5-mini"), "openai/gpt-5-mini"},
		{mk("anthropic/claude-3-5-sonnet-20241022", "anthropic/claude-3-5-haiku-20241022"), "anthropic/claude-3-5-haiku-20241022"},
		{mk("gemini/gemini-2.0-flash", "gemini/gemini-2.5-flash", "gemini/gemini-embedding-001"), "gemini/gemini-2.5-flash"},
		{mk("groq/llama-3.3-70b-versatile", "groq/whisper-large-v3"), "groq/llama-3.3-70b-versatile"},
		{mk("openai/whisper-1", "openai/dall-e-3"), ""},
	}
	for _, c := range cases {
		if got := PickAnalyst(c.models); got != c.want {
			t.Errorf("PickAnalyst = %q, want %q", got, c.want)
		}
	}
}
