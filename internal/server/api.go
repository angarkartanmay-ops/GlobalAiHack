package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/freshness"
	"github.com/angarkartanmay-ops/globalaihack/internal/rehearsal"
	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
	"github.com/angarkartanmay-ops/globalaihack/internal/store"
)

// Handler returns Heimdall's HTTP handler. ui, when not nil, is the built
// dashboard; mcp, when not nil, is mounted at /mcp.
func (a *App) Handler(ui fs.FS, mcp http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/status", a.handleStatus)

	guard := a.requireAuth
	mux.HandleFunc("GET /api/overview", guard(a.handleOverview))
	mux.HandleFunc("GET /api/model", guard(a.handleModel))
	mux.HandleFunc("POST /api/scan", guard(a.handleScan))
	mux.HandleFunc("GET /api/scans", guard(a.handleScans))
	mux.HandleFunc("POST /api/check", guard(a.handleCheck))
	mux.HandleFunc("GET /api/gateway/models", guard(a.handleGatewayModels))
	mux.HandleFunc("POST /api/rehearsals", guard(a.handleStartRehearsal))
	mux.HandleFunc("GET /api/rehearsals", guard(a.handleRehearsals))
	mux.HandleFunc("GET /api/rehearsal", guard(a.handleRehearsal))
	mux.HandleFunc("POST /api/migrations", guard(a.handleMigrate))
	mux.HandleFunc("GET /api/migrations", guard(a.handleMigrations))
	mux.HandleFunc("POST /api/freshness", guard(a.handleFreshness))
	mux.HandleFunc("GET /api/freshness", guard(a.handleFreshnessList))
	mux.HandleFunc("GET /api/events", guard(a.handleEvents))
	mux.HandleFunc("POST /api/traffic", guard(a.handleTraffic))
	mux.HandleFunc("POST /api/sim/retire", guard(a.handleSimRetire))

	if a.World != nil {
		mux.Handle("/sim/", http.StripPrefix("/sim", a.World.Handler()))
	}
	if mcp != nil {
		mux.Handle("/mcp", guardHandler(a, mcp))
	}
	if ui != nil {
		mux.Handle("/", spa(ui))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "Heimdall API is running. The dashboard was not bundled into this build.")
		})
	}
	return logRequests(a, mux)
}

// spa serves the dashboard, falling back to index.html for client routes.
func spa(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(ui, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func logRequests(a *App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet {
			a.Log.Info("api", "method", r.Method, "path", r.URL.Path, "ms", time.Since(start).Milliseconds())
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Access control
// ---------------------------------------------------------------------------

// locked reports whether the API refuses everyone: live mode with no token.
func (a *App) locked() bool {
	return a.Cfg.Mode == ModeLive && a.Cfg.AdminToken == "" && !a.Cfg.AllowAnonymous
}

// authorized reports whether a request may use the API. In simulator mode
// with no token set, everything is open: there is nothing real behind it.
func (a *App) authorized(r *http.Request) bool {
	if a.locked() {
		return false
	}
	if a.Cfg.AdminToken == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.Header.Get("X-Heimdall-Token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.Cfg.AdminToken)) == 1
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			if a.locked() {
				writeErr(w, http.StatusForbidden, "Heimdall is locked: set HEIMDALL_ADMIN_TOKEN and restart.")
				return
			}
			writeErr(w, http.StatusUnauthorized, "A valid admin token is required.")
			return
		}
		next(w, r)
	}
}

func guardHandler(a *App, h http.Handler) http.Handler {
	return a.requireAuth(h.ServeHTTP)
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"version": Version, "mode": a.Cfg.Mode, "simulated": a.Cfg.Mode == ModeSim,
		"auth_required": a.Cfg.AdminToken != "", "locked": a.locked(), "authenticated": a.authorized(r),
		"config_problems": a.Cfg.Validate(),
	}
	if !a.authorized(r) {
		writeJSON(w, 200, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	gw := map[string]any{"url": a.Cfg.BifrostURL, "healthy": false, "admin": false, "virtual_key": false}
	if err := a.GW.Health(ctx); err != nil {
		gw["error"] = "Gateway is not reachable: " + err.Error()
	} else {
		gw["healthy"] = true
		if err := a.GW.AdminReachable(ctx); err != nil {
			gw["error"] = "Admin API refused Heimdall's credentials: " + err.Error()
		} else {
			gw["admin"] = true
		}
		if ms, err := a.gatewayModels(ctx, false); err != nil {
			gw["error"] = "Virtual key was refused: " + err.Error()
		} else {
			gw["virtual_key"] = true
			gw["models"] = len(ms)
		}
	}
	out["gateway"] = gw

	tav := map[string]any{"configured": a.Web != nil}
	if a.Web != nil {
		tav["mode"] = a.Web.Mode()
		s, e := a.Web.Counts()
		tav["searches"], tav["extracts"] = s, e
	}
	a.mu.RLock()
	if a.lastWebErr != "" {
		tav["last_error"] = a.lastWebErr
	}
	out["scan"] = map[string]any{"running": a.scanning, "next": a.nextScan, "interval_hours": a.Cfg.ScanInterval.Hours()}
	if a.invErr != "" {
		out["inventory_error"] = a.invErr
	}
	a.mu.RUnlock()
	out["tavily"] = tav
	out["analyst"] = map[string]any{"model": a.analyst.Model, "judge": a.judge.Model, "auto": a.Cfg.AnalystModel == "", "calls": a.analyst.Calls() + a.judge.Calls()}
	if scans, err := store.List[core.Scan](a.Store, store.Scans, true, 1); err == nil && len(scans) > 0 {
		out["scan"].(map[string]any)["last"] = scans[0]
	}
	out["settings"] = map[string]any{
		"lookback_hours": a.Cfg.Lookback.Hours(), "rehearsal_samples": a.Cfg.RehearsalSamples, "rehearsal_budget_usd": a.Cfg.RehearsalBudget,
		"open_web": a.Cfg.OpenWeb, "auto_rollback": a.Cfg.AutoRollback, "alerts": a.Cfg.AlertWebhook != "",
		"traffic_keys": a.Cfg.TrafficKeys[0] != "",
	}
	if a.World != nil {
		out["sim"] = map[string]any{"traffic_sent": a.traffic.Sent.Load(), "traffic_failed": a.traffic.Failed.Load(), "retired": a.World.RetiredModels(),
			"examples": simExamples()}
	}
	writeJSON(w, 200, out)
}

// simExamples are answers the dashboard offers to check in simulator mode,
// where only the bundled pages are searchable.
func simExamples() []map[string]string {
	return []map[string]string{
		{"label": "Support answer with stale facts",
			"question": "Which API version should I use, and is there a weekend fee?",
			"answer":   "New integrations should use Northpost API v4, which is the current version. Weekend delivery carries a surcharge of $4.50 per parcel. Standard delivery takes 2 to 4 business days."},
		{"label": "Leadership answer",
			"question": "Who is the CEO of Northpost?",
			"answer":   "The Chief Executive Officer of Northpost is Daniel Okafor. You can return a parcel within 30 days of delivery."},
	}
}

// ---------------------------------------------------------------------------
// Overview and model detail
// ---------------------------------------------------------------------------

func (a *App) handleOverview(w http.ResponseWriter, r *http.Request) {
	exposures, inv := a.Exposures(r.Context())
	summary := map[string]any{"models": len(exposures), "at_risk": 0, "critical": 0, "share_at_risk": 0.0, "daily_cost_at_risk": 0.0,
		"daily_requests_at_risk": 0.0, "consumers_at_risk": 0, "unchecked": 0}
	consumers := map[string]bool{}
	var atRisk, critical, unchecked int
	var share, cost, reqs float64
	var nearest map[string]any
	for _, ex := range exposures {
		if ex.Finding == nil {
			unchecked++
			continue
		}
		if ex.Finding.Status != core.StatusDeprecated && ex.Finding.Status != core.StatusRetired {
			continue
		}
		atRisk++
		if ex.Risk == core.RiskCritical {
			critical++
		}
		if ex.Traffic != nil {
			share += ex.Traffic.RequestShare
			cost += ex.Traffic.DailyCost
			reqs += ex.Traffic.DailyReqs
			for _, c := range ex.Traffic.Consumers {
				consumers[c.Name] = true
			}
		}
		if ex.DaysLeft != nil && *ex.DaysLeft >= 0 && (nearest == nil || *ex.DaysLeft < nearest["days"].(int)) {
			nearest = map[string]any{"model": ex.ID(), "days": *ex.DaysLeft, "date": ex.Finding.ShutdownDate}
		}
	}
	summary["at_risk"], summary["critical"], summary["unchecked"] = atRisk, critical, unchecked
	summary["share_at_risk"], summary["daily_cost_at_risk"], summary["daily_requests_at_risk"] = share, cost, reqs
	summary["consumers_at_risk"] = len(consumers)
	if nearest != nil {
		summary["nearest"] = nearest
	}
	out := map[string]any{"summary": summary, "exposures": exposures, "scanning": a.Scanning(), "now": time.Now().UTC()}
	if inv != nil {
		out["window"] = map[string]any{"days": inv.WindowDays, "requests": inv.Requests, "cost": inv.Cost, "taken_at": inv.TakenAt, "lookback_hours": a.Cfg.Lookback.Hours()}
	}
	a.mu.RLock()
	if a.invErr != "" {
		out["inventory_error"] = a.invErr
	}
	a.mu.RUnlock()
	writeJSON(w, 200, out)
}

type rehearsalSummary struct {
	ID           string         `json:"id"`
	Baseline     string         `json:"baseline"`
	Candidate    string         `json:"candidate"`
	Status       string         `json:"status"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
	Requested    int            `json:"requested"`
	Sampled      int            `json:"sampled"`
	Completed    int            `json:"completed"`
	Settled      int            `json:"settled"`
	Outcomes     map[string]int `json:"outcomes"`
	Score        float64        `json:"score"`
	Verdict      string         `json:"verdict"`
	Summary      string         `json:"summary"`
	CostDeltaPct float64        `json:"cost_delta_pct"`
	LatencyDelta float64        `json:"latency_delta_pct"`
	Error        string         `json:"error,omitempty"`
}

func summarizeRehearsal(r core.Rehearsal) rehearsalSummary {
	return rehearsalSummary{ID: r.ID, Baseline: r.Baseline, Candidate: r.Candidate, Status: r.Status, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		Requested: r.Requested, Sampled: r.Sampled, Completed: r.Completed, Settled: len(r.Samples), Outcomes: r.Outcomes, Score: r.Score,
		Verdict: r.Verdict, Summary: r.Summary, CostDeltaPct: r.CostDeltaPct, LatencyDelta: r.LatencyDelta, Error: r.Error}
}

func (a *App) rehearsalSummaries(baseline string, limit int) []rehearsalSummary {
	out := []rehearsalSummary{}
	all, _ := store.List[core.Rehearsal](a.Store, store.Rehearsals, true, 300)
	for _, r := range all {
		if baseline != "" && r.Baseline != baseline {
			continue
		}
		out = append(out, summarizeRehearsal(r))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (a *App) handleModel(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, 400, "id is required")
		return
	}
	exposures, _ := a.Exposures(r.Context())
	var ex *core.Exposure
	for i := range exposures {
		if exposures[i].ID() == id {
			ex = &exposures[i]
		}
	}
	if ex == nil {
		e := core.Exposure{ModelRef: core.ParseModelRef(id), Risk: core.RiskNone}
		var f core.Finding
		if a.Store.Get(store.Findings, id, &f) == nil {
			e.Finding = &f
		}
		ex = &e
	}
	// Candidates: the stated replacement first, then the same provider's
	// other models, then everything else the gateway serves.
	ref := core.ParseModelRef(id)
	type cand struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	candidates := []cand{}
	seen := map[string]bool{id: true}
	if ex.Finding != nil && ex.Finding.Replacement != "" && ex.Finding.ReplacementOnGW {
		candidates = append(candidates, cand{ex.Finding.Replacement, "named by the provider as the replacement"})
		seen[ex.Finding.Replacement] = true
	}
	if ms, err := a.gatewayModels(r.Context(), false); err == nil {
		for _, pass := range []bool{true, false} {
			for _, m := range ms {
				same := core.ParseModelRef(m.ID).Provider == ref.Provider
				if seen[m.ID] || same != pass || (a.World != nil && m.ID == sim.AnalystModel) {
					continue
				}
				seen[m.ID] = true
				reason := "another provider on this gateway"
				if same {
					reason = "same provider"
				}
				// Do not offer a model that is itself on the way out.
				var cf core.Finding
				if a.Store.Get(store.Findings, m.ID, &cf) == nil {
					if cf.Status == core.StatusRetired {
						continue
					}
					if cf.Status == core.StatusDeprecated {
						reason += "; itself scheduled for shutdown"
						if cf.ShutdownDate != "" {
							reason += " on " + cf.ShutdownDate
						}
					}
				}
				candidates = append(candidates, cand{m.ID, reason})
			}
		}
	}
	var mig *core.Migration
	var m core.Migration
	if a.Store.Get(store.Migrations, id, &m) == nil {
		mig = &m
	}
	writeJSON(w, 200, map[string]any{"exposure": ex, "candidates": candidates, "rehearsals": a.rehearsalSummaries(id, 10), "migration": mig})
}

// ---------------------------------------------------------------------------
// Scans
// ---------------------------------------------------------------------------

func (a *App) handleScan(w http.ResponseWriter, _ *http.Request) {
	if a.Scanning() {
		writeErr(w, http.StatusConflict, "A scan is already running.")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		if _, err := a.RunScan(ctx, "manual"); err != nil {
			a.Log.Warn("manual scan failed", "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (a *App) handleScans(w http.ResponseWriter, _ *http.Request) {
	scans, _ := store.List[core.Scan](a.Store, store.Scans, true, 20)
	writeJSON(w, 200, map[string]any{"scans": scans})
}

// handleCheck runs the radar on one model on demand, whether or not it is
// in current traffic.
func (a *App) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if err := readJSON(r, &req); err != nil || !strings.Contains(req.Model, "/") {
		writeErr(w, 400, "model must be provider/model")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	f, err := a.CheckModel(ctx, req.Model)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

// CheckModel runs the radar on one model and stores the finding.
func (a *App) CheckModel(ctx context.Context, model string) (core.Finding, error) {
	if err := a.ensureAnalyst(ctx); err != nil {
		return core.Finding{}, err
	}
	var prev *core.Finding
	var p core.Finding
	if a.Store.Get(store.Findings, model, &p) == nil {
		prev = &p
	}
	a.radar.Reset()
	f := a.radar.Check(ctx, core.ParseModelRef(model), prev, "manual")
	_ = a.Store.Put(store.Findings, model, f)
	return f, nil
}

func (a *App) handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	ms, err := a.gatewayModels(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	writeJSON(w, 200, map[string]any{"models": ids})
}

// ---------------------------------------------------------------------------
// Rehearsals
// ---------------------------------------------------------------------------

func (a *App) handleStartRehearsal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Baseline  string  `json:"baseline"`
		Candidate string  `json:"candidate"`
		Samples   int     `json:"samples"`
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !strings.Contains(req.Baseline, "/") || !strings.Contains(req.Candidate, "/") {
		writeErr(w, 400, "baseline and candidate must be provider/model")
		return
	}
	if req.Samples <= 0 {
		req.Samples = a.Cfg.RehearsalSamples
	}
	if req.Samples > 200 {
		req.Samples = 200
	}
	if req.BudgetUSD <= 0 {
		req.BudgetUSD = a.Cfg.RehearsalBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.ensureAnalyst(ctx); err != nil {
		a.Log.Warn("no judge model; rehearsal will use deterministic checks only", "err", err)
	}
	id := a.Store.NewID("reh")
	opt := rehearsal.Options{ID: id, Baseline: req.Baseline, Candidate: req.Candidate, Samples: req.Samples, BudgetUSD: req.BudgetUSD}
	_ = a.Store.Put(store.Rehearsals, id, core.Rehearsal{ID: id, Baseline: req.Baseline, Candidate: req.Candidate, Status: "running",
		StartedAt: time.Now().UTC(), Requested: req.Samples, Outcomes: map[string]int{}, CheckFailures: map[string]int{}, Samples: []core.Sample{}})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		reh := a.runner.Run(ctx, opt, func(p core.Rehearsal) { _ = a.Store.Put(store.Rehearsals, id, p) })
		_ = a.Store.Put(store.Rehearsals, id, reh)
		_ = a.Store.Trim(store.Rehearsals, 60)
		level := "info"
		title := fmt.Sprintf("Rehearsal finished: %s as a replacement for %s scored %.0f%%", reh.Candidate, reh.Baseline, reh.Score)
		if reh.Status == "failed" {
			level, title = "warn", fmt.Sprintf("Rehearsal of %s for %s failed", reh.Candidate, reh.Baseline)
			reh.Summary = reh.Error
		}
		a.Event(core.Event{Kind: "rehearsal", Level: level, Model: reh.Baseline, Title: title, Detail: reh.Summary})
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (a *App) handleRehearsals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"rehearsals": a.rehearsalSummaries(r.URL.Query().Get("baseline"), 50)})
}

func (a *App) handleRehearsal(w http.ResponseWriter, r *http.Request) {
	var reh core.Rehearsal
	if err := a.Store.Get(store.Rehearsals, r.URL.Query().Get("id"), &reh); err != nil {
		writeErr(w, 404, "rehearsal not found")
		return
	}
	writeJSON(w, 200, reh)
}

// ---------------------------------------------------------------------------
// Migrations
// ---------------------------------------------------------------------------

func (a *App) handleMigrate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model       string `json:"model"`
		Replacement string `json:"replacement"`
		Stage       string `json:"stage"`
		CanaryPct   int    `json:"canary_pct"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !strings.Contains(req.Model, "/") {
		writeErr(w, 400, "model must be provider/model")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var mig core.Migration
	if a.Store.Get(store.Migrations, req.Model, &mig) != nil {
		mig = core.Migration{Model: req.Model, Stage: core.StageNone}
	}
	var err error
	if req.Stage == core.StageNone || req.Stage == "" {
		mig, err = a.cut.Rollback(ctx, mig)
		if err == nil {
			a.Event(core.Event{Kind: "migration", Level: "info", Model: req.Model,
				Title: fmt.Sprintf("Routing rule for %s removed", req.Model), Detail: "The gateway routes this model exactly as it did before Heimdall's rule."})
		}
	} else {
		if req.Replacement == "" {
			req.Replacement = mig.Replacement
		}
		if !strings.Contains(req.Replacement, "/") {
			writeErr(w, 400, "replacement must be provider/model")
			return
		}
		mig.Replacement = req.Replacement
		mig, err = a.cut.Apply(ctx, mig, req.Stage, req.CanaryPct)
		if err == nil {
			what := map[string]string{
				core.StageArmed:    fmt.Sprintf("Safety net armed: if %s fails, %s takes the request", req.Model, req.Replacement),
				core.StageCanary:   fmt.Sprintf("Canary started: %d%% of %s traffic now goes to %s", req.CanaryPct, req.Model, req.Replacement),
				core.StagePromoted: fmt.Sprintf("Cut over: requests for %s are now served by %s", req.Model, req.Replacement),
			}[req.Stage]
			a.Event(core.Event{Kind: "migration", Level: "info", Model: req.Model, Title: what,
				Detail: "Applied as Bifrost routing rule " + mig.RuleName + "."})
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(mig.History) > 30 {
		mig.History = mig.History[len(mig.History)-30:]
	}
	_ = a.Store.Put(store.Migrations, req.Model, mig)
	writeJSON(w, 200, mig)
}

func (a *App) handleMigrations(w http.ResponseWriter, _ *http.Request) {
	migs, _ := store.List[core.Migration](a.Store, store.Migrations, false, 0)
	sort.Slice(migs, func(i, j int) bool { return migs[i].UpdatedAt.After(migs[j].UpdatedAt) })
	writeJSON(w, 200, map[string]any{"migrations": migs})
}

// ---------------------------------------------------------------------------
// Freshness
// ---------------------------------------------------------------------------

// CheckAnswer runs the stale-answer detector and stores the report.
func (a *App) CheckAnswer(ctx context.Context, req freshness.Request) (core.FreshnessReport, error) {
	if a.fresh == nil {
		return core.FreshnessReport{}, errors.New("Tavily is not configured, so answers cannot be checked against the web")
	}
	if err := a.ensureAnalyst(ctx); err != nil {
		return core.FreshnessReport{}, err
	}
	rep := a.fresh.Check(ctx, req)
	rep.ID = a.Store.NewID("fresh")
	_ = a.Store.Put(store.Freshness, rep.ID, rep)
	_ = a.Store.Trim(store.Freshness, 100)
	return rep, nil
}

func (a *App) handleFreshness(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Model    string `json:"model"`
		Rewrite  bool   `json:"rewrite"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(req.Answer) == "" {
		writeErr(w, 400, "answer is required")
		return
	}
	if len(req.Answer) > 20000 {
		writeErr(w, 400, "answer is too long (limit 20,000 characters)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	rep, err := a.CheckAnswer(ctx, freshness.Request{Question: req.Question, Answer: req.Answer, Model: req.Model, Source: "manual", Rewrite: req.Rewrite, Run: "manual"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, rep)
}

func (a *App) handleFreshnessList(w http.ResponseWriter, _ *http.Request) {
	reps, _ := store.List[core.FreshnessReport](a.Store, store.Freshness, true, 20)
	writeJSON(w, 200, map[string]any{"reports": reps})
}

func (a *App) handleEvents(w http.ResponseWriter, _ *http.Request) {
	evs, _ := store.List[core.Event](a.Store, store.Events, true, 100)
	writeJSON(w, 200, map[string]any{"events": evs})
}

// ---------------------------------------------------------------------------
// Sample traffic and simulator controls
// ---------------------------------------------------------------------------

// handleTraffic sends the bundled sample workload through the gateway under
// the demo applications' virtual keys, so a fresh gateway has realistic
// traffic to inventory and rehearse. Models are whatever the caller names.
func (a *App) handleTraffic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Large  string `json:"large"`
		Small  string `json:"small"`
		Other  string `json:"other"`
		Legacy string `json:"legacy"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if a.Cfg.TrafficKeys[0] == "" {
		writeErr(w, 400, "No sample-application virtual keys are configured (HEIMDALL_TRAFFIC_KEYS).")
		return
	}
	if req.Large == "" {
		writeErr(w, 400, "Name at least the main model to send sample traffic to (large).")
		return
	}
	a.mu.Lock()
	if a.trafficBusy {
		a.mu.Unlock()
		writeErr(w, http.StatusConflict, "Sample traffic is already being sent.")
		return
	}
	a.trafficBusy = true
	a.mu.Unlock()
	items := sim.Workload(req.Large, req.Small, req.Other, req.Legacy, a.Cfg.TrafficKeys)
	go func() {
		defer func() {
			a.mu.Lock()
			a.trafficBusy = false
			a.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		var st sim.Stats
		sim.Send(ctx, a.GW, items, 4, &st)
		a.traffic.Sent.Add(st.Sent.Load())
		a.traffic.Failed.Add(st.Failed.Load())
		a.Event(core.Event{Kind: "traffic", Level: "info", Title: fmt.Sprintf("Sample traffic sent: %d requests, %d failed", st.Sent.Load(), st.Failed.Load()),
			Detail: "Sent under the sample applications' virtual keys. Run a scan to see the models it used."})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "requests": len(items)})
}

// handleSimRetire shuts a simulated model down (or brings it back), to show
// what a routing rule does when a provider retires a model for real.
func (a *App) handleSimRetire(w http.ResponseWriter, r *http.Request) {
	if a.World == nil {
		writeErr(w, 400, "Only available in simulator mode.")
		return
	}
	var req struct {
		Model  string `json:"model"`
		Revive bool   `json:"revive"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	name := core.ParseModelRef(req.Model).Model
	if req.Revive {
		a.World.Revive(name)
		a.Event(core.Event{Kind: "sim", Level: "info", Model: req.Model, Title: "Simulator: " + req.Model + " is serving again"})
	} else {
		if !a.World.Retire(name) {
			writeErr(w, 404, "unknown simulated model")
			return
		}
		a.Event(core.Event{Kind: "sim", Level: "warn", Model: req.Model, Title: "Simulator: the provider has shut down " + req.Model,
			Detail: "Requests to it now return 404 model_not_found from the simulated provider."})
	}
	writeJSON(w, 200, map[string]any{"retired": a.World.RetiredModels()})
}
