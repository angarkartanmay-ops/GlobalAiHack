//go:build e2e

// Package e2e runs Heimdall against a real Bifrost gateway binary.
//
//	BIFROST_BIN=/path/to/bifrost-http go test -tags e2e -count=1 -v ./e2e
//
// The gateway is the real thing, started from the config.json that
// `heimdall bootstrap` writes. Only what sits behind it is simulated: the
// model providers and the web. Nothing here needs an API key or network
// access. The suite is skipped unless BIFROST_BIN is set.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/freshness"
	"github.com/angarkartanmay-ops/globalaihack/internal/mcpserver"
	"github.com/angarkartanmay-ops/globalaihack/internal/server"
	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
)

const dashboardToken = "e2e-dashboard-token"

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type stack struct {
	app        *server.App
	heimdall   string
	gateway    string
	adminUser  string
	adminPass  string
	gatewayLog string
}

func (s *stack) call(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, s.heimdall+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+dashboardToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	return resp.StatusCode
}

// gatewayAdmin calls the gateway's own admin API, to check what Heimdall did
// from the gateway's side.
func (s *stack) gatewayAdmin(t *testing.T, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", s.gateway+path, nil)
	req.SetBasicAuth(s.adminUser, s.adminPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return resp.StatusCode
}

func start(t *testing.T) *stack {
	bin := os.Getenv("BIFROST_BIN")
	if bin == "" {
		t.Skip("BIFROST_BIN is not set; skipping the real-gateway suite")
	}
	dir := t.TempDir()
	hp, gp := freePort(t), freePort(t)
	s := &stack{heimdall: fmt.Sprintf("http://127.0.0.1:%d", hp), gateway: fmt.Sprintf("http://127.0.0.1:%d", gp),
		gatewayLog: filepath.Join(dir, "gateway.log")}

	env := map[string]string{"HEIMDALL_ADMIN_TOKEN": dashboardToken}
	res, err := server.Bootstrap(server.BootstrapOptions{Dir: filepath.Join(dir, "gateway"), Mode: server.ModeSim,
		HeimdallURL: s.heimdall, GatewayURL: s.gateway, Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	s.adminUser, s.adminPass = res.Env["BIFROST_ADMIN_USERNAME"], res.Env["BIFROST_ADMIN_PASSWORD"]

	cfg := server.Config{Mode: server.ModeSim, DataDir: filepath.Join(dir, "heimdall"), BifrostURL: s.gateway,
		AdminUser: s.adminUser, AdminPass: s.adminPass, VirtualKey: res.Env["BIFROST_VIRTUAL_KEY"],
		AnalystModel: sim.AnalystModel, JudgeModel: sim.AnalystModel, TavilyRelay: "tavily", OpenWeb: true,
		Lookback: 24 * time.Hour, RehearsalSamples: 40, RehearsalBudget: 1, SimKey: res.Env["HEIMDALL_SIM_KEY"], AdminToken: dashboardToken}
	for i, c := range sim.Consumers {
		cfg.TrafficKeys[i] = c.Value
	}
	app, err := server.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.World.Fast = true
	s.app = app
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", hp), Handler: app.Handler(nil, mcpserver.New(app))}
	go func() { _ = srv.ListenAndServe() }()

	logf, _ := os.Create(s.gatewayLog)
	cmd := exec.Command(bin, "-app-dir", filepath.Join(dir, "gateway"), "-port", fmt.Sprint(gp), "-host", "127.0.0.1", "-log-style", "pretty")
	cmd.Env = os.Environ()
	for k, v := range res.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = srv.Close()
		_ = app.Close()
		logf.Close()
		if t.Failed() {
			if data, err := os.ReadFile(s.gatewayLog); err == nil {
				lines := strings.Split(string(data), "\n")
				if len(lines) > 40 {
					lines = lines[len(lines)-40:]
				}
				t.Logf("gateway log tail:\n%s", strings.Join(lines, "\n"))
			}
		}
	})

	deadline := time.Now().Add(90 * time.Second)
	for {
		if err := app.GW.Health(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gateway did not become healthy within 90 seconds")
		}
		time.Sleep(300 * time.Millisecond)
	}
	return s
}

// waitFor polls until cond holds; the gateway writes its logs asynchronously.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestHeimdallAgainstRealBifrost(t *testing.T) {
	s := start(t)
	ctx := context.Background()
	const old, repl = "aurora/aurora-large-1", "aurora/aurora-large-2"

	// --- applications send traffic through the gateway
	var st sim.Stats
	items := sim.DefaultWorkload()
	sim.Send(ctx, s.app.GW, items, 8, &st)
	if st.Sent.Load() != int64(len(items)) || st.Failed.Load() != 4 {
		t.Fatalf("sent=%d failed=%d; only the four requests to the retired model should fail", st.Sent.Load(), st.Failed.Load())
	}
	waitFor(t, "the gateway to log the workload", func() bool {
		inv, err := s.app.RefreshInventory(ctx)
		return err == nil && inv.Requests >= len(items)
	})

	// --- the dashboard API is closed without the token
	if resp, err := http.Get(s.heimdall + "/api/overview"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the API must require the dashboard token")
	}

	// --- scan: inventory from gateway aggregates, evidence through the relay
	if _, err := s.app.RunScan(ctx, "e2e"); err != nil {
		t.Fatal(err)
	}
	var ov struct {
		Summary   map[string]any  `json:"summary"`
		Exposures []core.Exposure `json:"exposures"`
	}
	s.call(t, "GET", "/api/overview", nil, &ov)
	byID := map[string]core.Exposure{}
	for _, e := range ov.Exposures {
		byID[e.ID()] = e
	}
	if len(byID) != 5 || ov.Summary["at_risk"].(float64) != 4 {
		t.Fatalf("expected 5 models with 4 at risk, got %d: %v", len(byID), ov.Summary)
	}
	large := byID[old]
	if large.Finding.Status != core.StatusDeprecated || *large.DaysLeft != 23 || large.Finding.Confidence != core.ConfidenceHigh || large.Finding.Conflict == nil {
		t.Errorf("aurora-large-1: %+v", large.Finding)
	}
	if large.Finding.Replacement != repl || !large.Finding.ReplacementOnGW {
		t.Errorf("replacement: %q on gateway %v", large.Finding.Replacement, large.Finding.ReplacementOnGW)
	}
	if len(large.Traffic.Consumers) != 2 || large.Traffic.Consumers[0].Name != "checkout-app" || large.Traffic.Cost <= 0 {
		t.Errorf("traffic breakdown by virtual key: %+v", large.Traffic)
	}
	mini := byID["aurora/aurora-mini-1"].Finding
	if mini.Evidence[0].Kind != "gateway" || mini.Discarded != 1 {
		t.Errorf("aurora-mini-1: the gateway's shutdown_date should lead, and one misquote should be discarded: %+v", mini)
	}
	if e := byID["aurora/aurora-instant-0"]; e.Risk != core.RiskCritical || e.Traffic.SuccessRate != 0 {
		t.Errorf("aurora-instant-0: %s success=%.0f", e.Risk, e.Traffic.SuccessRate)
	}

	// --- Heimdall's Tavily calls went through the gateway's relay and were logged there
	var relayLogs struct {
		Logs []map[string]any `json:"logs"`
	}
	waitFor(t, "relay logs", func() bool {
		s.gatewayAdmin(t, "/api/relay-logs?limit=5", &relayLogs)
		return len(relayLogs.Logs) > 0
	})

	// --- rehearsal: replay real logged requests against the candidate
	var started struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/rehearsals", map[string]any{"baseline": old, "candidate": repl, "samples": 200}, &started); code != http.StatusAccepted {
		t.Fatalf("start rehearsal: %d", code)
	}
	var reh core.Rehearsal
	waitFor(t, "the rehearsal to finish", func() bool {
		s.call(t, "GET", "/api/rehearsal?id="+started.ID, nil, &reh)
		return reh.Status != "running"
	})
	if reh.Status != "done" || reh.Verdict != core.ReadyCaveats || reh.CheckFailures["json_output"] != 4 || reh.CheckFailures["tool_calls"] != 2 {
		t.Fatalf("rehearsal: %s %s score=%.1f failures=%v err=%s", reh.Status, reh.Verdict, reh.Score, reh.CheckFailures, reh.Error)
	}
	if reh.FresherCount == 0 || reh.StalerCount != 0 || reh.CostDeltaPct >= 0 || reh.SpendUSD <= 0 {
		t.Errorf("fresher=%d staler=%d cost=%.1f%% spend=%f", reh.FresherCount, reh.StalerCount, reh.CostDeltaPct, reh.SpendUSD)
	}

	// --- replays and analyst calls must not show up as demand
	waitFor(t, "replay logs to be written", func() bool {
		p, err := s.app.GW.Logs(ctx, bifrost.LogFilter{Models: []string{"aurora-large-2"}, Limit: 1})
		return err == nil && p.Pagination.TotalCount >= reh.Sampled
	})
	inv, _ := s.app.RefreshInventory(ctx)
	for _, it := range inv.Items {
		if it.ID() == repl || it.ID() == sim.AnalystModel {
			t.Errorf("%s is in the inventory, but only Heimdall called it", it.ID())
		}
		for _, c := range it.Consumers {
			if c.Name == "heimdall" {
				t.Errorf("heimdall is listed as a consumer of %s", it.ID())
			}
		}
	}

	// --- cutover, on the real routing engine
	ask := func() (string, bool) {
		msgs, _ := json.Marshal([]map[string]string{{"role": "system", "content": sim.SysSupport}, {"role": "user", "content": "What is the weekend delivery surcharge?"}})
		resp, err := s.app.GW.ChatAs(ctx, bifrost.ChatRequest{Model: old, Messages: msgs}, sim.Consumers[1].Value, nil)
		if err != nil {
			return err.Error(), false
		}
		return resp.Model, true
	}
	s.app.World.Retire("aurora-large-1")
	if got, ok := ask(); ok {
		t.Fatalf("with no rule, a provider shutdown must reach the application; got %s", got)
	}
	var mig core.Migration
	if code := s.call(t, "POST", "/api/migrations", map[string]any{"model": old, "replacement": repl, "stage": "armed"}, &mig); code != 200 {
		t.Fatalf("arm: %d", code)
	}
	if got, ok := ask(); !ok || got != "aurora-large-2" {
		t.Fatalf("with the safety net armed, the request should be served by the replacement; got %s ok=%v", got, ok)
	}
	s.app.World.Revive("aurora-large-1")

	if code := s.call(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "canary", "canary_pct": 30}, &mig); code != 200 {
		t.Fatalf("canary: %d", code)
	}
	served := map[string]int{}
	for i := 0; i < 120; i++ {
		got, ok := ask()
		if !ok {
			t.Fatal(got)
		}
		served[got]++
	}
	if n := served["aurora-large-2"]; n < 18 || n > 56 {
		t.Errorf("canary split = %v; expected roughly 30%% on the replacement", served)
	}
	var health *core.CanaryHealth
	waitFor(t, "canary health to reflect the logged requests", func() bool {
		var d struct {
			Migration core.Migration `json:"migration"`
		}
		s.call(t, "GET", "/api/model?id="+old, nil, &d)
		h, err := s.app.CutoverHealth(ctx, d.Migration)
		health = h
		return err == nil && h != nil && h.OldRequests+h.NewRequests >= 120
	})
	if health.NewRequests != served["aurora-large-2"] || health.NewSuccess != 100 {
		t.Errorf("health %+v does not match what the applications saw %v", health, served)
	}

	if code := s.call(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "promoted"}, &mig); code != 200 {
		t.Fatalf("promote: %d", code)
	}
	if got, _ := ask(); got != "aurora-large-2" {
		t.Errorf("promoted: served by %s", got)
	}
	var rules struct {
		Count int `json:"count"`
	}
	s.gatewayAdmin(t, "/api/routing/rules", &rules)
	if rules.Count != 1 {
		t.Errorf("one migration should be one routing rule on the gateway, found %d", rules.Count)
	}
	if code := s.call(t, "POST", "/api/migrations", map[string]any{"model": old, "stage": "none"}, &mig); code != 200 {
		t.Fatalf("rollback: %d", code)
	}
	s.gatewayAdmin(t, "/api/routing/rules", &rules)
	if got, _ := ask(); got != "aurora-large-1" || rules.Count != 0 {
		t.Errorf("after rollback: served by %s, %d rules left", got, rules.Count)
	}

	// --- the stale-answer check, evidence through the relay
	rep, err := s.app.CheckAnswer(ctx, freshness.Request{Answer: "New integrations should use Northpost API v4, which is the current version. Standard delivery takes 2 to 4 business days.", Rewrite: true})
	if err != nil || rep.Changed != 1 || rep.Supported != 1 || !strings.Contains(rep.Rewrite, "v5 [1]") {
		t.Errorf("freshness: changed=%d supported=%d rewrite=%q err=%v", rep.Changed, rep.Supported, rep.Rewrite, err)
	}

	// --- the gateway connected to Heimdall's MCP server and sees its tools
	var clients []map[string]any
	var raw json.RawMessage
	waitFor(t, "the gateway to connect to Heimdall's MCP server", func() bool {
		if s.gatewayAdmin(t, "/api/mcp/clients", &raw) != 200 {
			return false
		}
		if json.Unmarshal(raw, &clients) != nil {
			var wrapped struct {
				Clients []map[string]any `json:"clients"`
			}
			_ = json.Unmarshal(raw, &wrapped)
			clients = wrapped.Clients
		}
		return strings.Contains(string(raw), "heimdall_list_sunsets")
	})
	t.Logf("gateway MCP clients: %.600s", raw)
}
