package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/analyst"
	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/cutover"
	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
	"github.com/angarkartanmay-ops/globalaihack/internal/freshness"
	"github.com/angarkartanmay-ops/globalaihack/internal/radar"
	"github.com/angarkartanmay-ops/globalaihack/internal/rehearsal"
	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
	"github.com/angarkartanmay-ops/globalaihack/internal/store"
	"github.com/angarkartanmay-ops/globalaihack/internal/tavily"
)

// Version is set at build time.
var Version = "dev"

// App is a running Heimdall.
type App struct {
	Cfg   Config
	Log   *slog.Logger
	Store *store.Store
	GW    *bifrost.Client
	Web   tavily.Client
	World *sim.World // nil in live mode

	analyst *analyst.Analyst
	judge   *analyst.Analyst
	radar   *radar.Radar
	fresh   *freshness.Checker
	runner  *rehearsal.Runner
	cut     *cutover.Manager

	mu          sync.RWMutex
	inv         *core.Inventory
	invErr      string
	scanning    bool
	nextScan    time.Time
	gwModels    []bifrost.ModelInfo
	gwModelsAt  time.Time
	lastWebErr  string
	traffic     sim.Stats
	trafficBusy bool
	startedAt   time.Time
}

// New builds an App from configuration.
func New(cfg Config, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Log: log, Store: st, startedAt: time.Now()}
	a.GW = bifrost.New(cfg.BifrostURL, cfg.VirtualKey, cfg.AdminUser, cfg.AdminPass)

	if cfg.TavilyRelay != "" {
		a.Web = tavily.NewViaRelay(a.GW, cfg.TavilyRelay, func(err error) (int, string, bool) {
			var ae *bifrost.APIError
			if errors.As(err, &ae) {
				msg := ae.Message
				if msg == "" {
					msg = ae.Body
				}
				return ae.Status, msg, true
			}
			return 0, "", false
		})
	} else if cfg.TavilyAPIKey != "" {
		a.Web = tavily.NewDirect(cfg.TavilyAPIKey, cfg.TavilyBaseURL)
	}

	reg, err := evidence.LoadRegistry(cfg.SourcesFile)
	if err != nil {
		log.Warn("could not read HEIMDALL_SOURCES_FILE; using built-in sources", "err", err)
	}
	if cfg.Mode == ModeSim {
		a.World = sim.NewWorld(time.Now())
		a.World.Key = cfg.SimKey
		for k, v := range sim.Registry() {
			reg[k] = v
		}
	}

	a.analyst = analyst.New(a.GW, cfg.AnalystModel, cfg.AnalystFallbacks, "radar")
	a.judge = analyst.New(a.GW, cfg.JudgeModel, cfg.AnalystFallbacks, "judge")
	a.radar = radar.New(a.GW, a.Web, a.analyst, reg, log)
	a.radar.OpenWeb = cfg.OpenWeb
	if a.Web != nil {
		a.fresh = freshness.New(a.Web, a.analyst, log)
	}
	a.runner = &rehearsal.Runner{GW: a.GW, Judge: a.judge, Fresh: a.fresh, Now: time.Now, Log: log, Lookback: cfg.Lookback, Concurrency: 4}
	a.cut = cutover.New(a.GW)
	return a, nil
}

// CutoverHealth measures a migration's live traffic from gateway logs.
func (a *App) CutoverHealth(ctx context.Context, m core.Migration) (*core.CanaryHealth, error) {
	return a.cut.Health(ctx, m)
}

// Close releases resources.
func (a *App) Close() error { return a.Store.Close() }

// ---------------------------------------------------------------------------
// Analyst model selection
// ---------------------------------------------------------------------------

// analystPreference ranks model names for Heimdall's own reasoning when the
// operator has not chosen one: small, inexpensive chat models. The patterns
// are deliberately loose about version numbers. A list of exact model names
// would be the very kind of stale configuration Heimdall exists to catch.
var analystPreference = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(openai)/gpt-[\d.]+o?-mini$`),
	regexp.MustCompile(`(?i)^(anthropic)/claude-.*haiku`),
	regexp.MustCompile(`(?i)^(gemini)/gemini-[\d.]+-flash$`),
	regexp.MustCompile(`(?i)^(openai)/gpt-[\d.]+o?$`),
	regexp.MustCompile(`(?i)^(anthropic)/claude-.*sonnet`),
	regexp.MustCompile(`(?i)^(gemini)/gemini-[\d.]+-(flash|pro)`),
	regexp.MustCompile(`(?i)^(groq|cerebras|mistral|deepseek|xai|openrouter|cohere)/`),
}

var notChat = regexp.MustCompile(`(?i)embed|whisper|tts|audio|image|dall-e|moderation|realtime|transcribe|rerank|guard|vision-preview|search`)

// PickAnalyst chooses an analyst model from the gateway's model list: the
// newest model matching the highest-ranked preference.
func PickAnalyst(models []bifrost.ModelInfo) string {
	for _, re := range analystPreference {
		best, bestCreated := "", int64(-1)
		for _, m := range models {
			if !re.MatchString(m.ID) || notChat.MatchString(m.ID) {
				continue
			}
			if m.Created > bestCreated || (m.Created == bestCreated && m.ID > best) {
				best, bestCreated = m.ID, m.Created
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

// ensureAnalyst makes sure an analyst model is set, choosing one if needed.
func (a *App) ensureAnalyst(ctx context.Context) error {
	if a.analyst.Model != "" {
		return nil
	}
	models, err := a.gatewayModels(ctx, true)
	if err != nil {
		return fmt.Errorf("cannot list gateway models to choose an analyst model: %w", err)
	}
	pick := PickAnalyst(models)
	if pick == "" {
		return errors.New("no suitable chat model found on the gateway; set HEIMDALL_ANALYST_MODEL to a provider/model the gateway serves")
	}
	a.mu.Lock()
	a.analyst.Model = pick
	if a.judge.Model == "" {
		a.judge.Model = pick
	}
	a.mu.Unlock()
	a.Log.Info("analyst model chosen from the gateway's model list", "model", pick)
	return nil
}

func (a *App) gatewayModels(ctx context.Context, fresh bool) ([]bifrost.ModelInfo, error) {
	a.mu.RLock()
	cached, at := a.gwModels, a.gwModelsAt
	a.mu.RUnlock()
	if !fresh && cached != nil && time.Since(at) < 2*time.Minute {
		return cached, nil
	}
	ms, err := a.GW.ListModels(ctx)
	if err != nil {
		if cached != nil {
			return cached, nil
		}
		return nil, err
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	a.mu.Lock()
	a.gwModels, a.gwModelsAt = ms, time.Now()
	a.mu.Unlock()
	return ms, nil
}

// ---------------------------------------------------------------------------
// Inventory and scans
// ---------------------------------------------------------------------------

// RefreshInventory reads current traffic by model from the gateway.
func (a *App) RefreshInventory(ctx context.Context) (*core.Inventory, error) {
	inv, err := radar.BuildInventory(ctx, a.GW, a.Cfg.Lookback, time.Now())
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.invErr = err.Error()
		return a.inv, err
	}
	a.inv, a.invErr = inv, ""
	return inv, nil
}

// Inventory returns the cached inventory, refreshing it when stale.
func (a *App) Inventory(ctx context.Context, maxAge time.Duration) *core.Inventory {
	a.mu.RLock()
	inv := a.inv
	a.mu.RUnlock()
	if inv != nil && time.Since(inv.TakenAt) < maxAge {
		return inv
	}
	if fresh, err := a.RefreshInventory(ctx); err == nil {
		return fresh
	}
	return inv
}

// maxScanModels bounds one scan. Models are checked in order of traffic.
const maxScanModels = 60

// Scanning reports whether a scan is in progress.
func (a *App) Scanning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.scanning
}

// RunScan checks every model in the inventory. It returns immediately with
// an error if a scan is already running.
func (a *App) RunScan(ctx context.Context, trigger string) (*core.Scan, error) {
	a.mu.Lock()
	if a.scanning {
		a.mu.Unlock()
		return nil, errors.New("a scan is already running")
	}
	a.scanning = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.scanning = false
		a.mu.Unlock()
	}()

	scan := &core.Scan{ID: a.Store.NewID("scan"), StartedAt: time.Now().UTC(), Status: "running", Trigger: trigger}
	_ = a.Store.Put(store.Scans, scan.ID, scan)
	finish := func(err error) (*core.Scan, error) {
		scan.FinishedAt = time.Now().UTC()
		scan.Status = "done"
		if err != nil {
			scan.Status, scan.Error = "failed", err.Error()
		}
		_ = a.Store.Put(store.Scans, scan.ID, scan)
		_ = a.Store.Trim(store.Scans, 100)
		return scan, err
	}

	inv, err := a.RefreshInventory(ctx)
	if err != nil {
		return finish(fmt.Errorf("reading traffic from the gateway: %w", err))
	}
	if len(inv.Items) == 0 {
		return finish(nil)
	}
	if err := a.ensureAnalyst(ctx); err != nil {
		return finish(err)
	}
	var s0, e0 int64
	if a.Web != nil {
		s0, e0 = a.Web.Counts()
	}
	l0 := a.analyst.Calls()
	a.radar.Reset()

	items := inv.Items
	if len(items) > maxScanModels {
		items = items[:maxScanModels]
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	var mu sync.Mutex
	var firstErr string
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it core.InventoryItem) {
			defer wg.Done()
			defer func() { <-sem }()
			var prev *core.Finding
			var p core.Finding
			if a.Store.Get(store.Findings, it.ID(), &p) == nil {
				prev = &p
			}
			f := a.radar.Check(ctx, it.ModelRef, prev, scan.ID)
			// Keep the last good finding when a check fails outright.
			if f.Status == core.StatusUnknown && prev != nil && prev.Status != core.StatusUnknown {
				prev.Error = f.Error
				f = *prev
			}
			_ = a.Store.Put(store.Findings, it.ID(), f)
			mu.Lock()
			scan.Models++
			if f.Status == core.StatusDeprecated || f.Status == core.StatusRetired {
				scan.AtRisk++
			}
			if f.Error != "" && firstErr == "" {
				firstErr = f.Error
			}
			mu.Unlock()
			a.announce(it, prev, &f)
		}(it)
	}
	wg.Wait()

	if a.Web != nil {
		s1, e1 := a.Web.Counts()
		scan.Searches, scan.Extracts = int(s1-s0), int(e1-e0)
	}
	scan.LLMCalls = int(a.analyst.Calls() - l0)
	a.mu.Lock()
	a.lastWebErr = firstErr
	a.mu.Unlock()
	return finish(nil)
}

// announce turns what a scan learned about one model into activity events.
func (a *App) announce(it core.InventoryItem, prev, f *core.Finding) {
	atRisk := f.Status == core.StatusDeprecated || f.Status == core.StatusRetired
	ex := radar.Exposure(it.ModelRef, f, &it, time.Now())
	level := "info"
	switch ex.Risk {
	case core.RiskCritical:
		level = "critical"
	case core.RiskHigh, core.RiskMedium:
		level = "warn"
	}
	traffic := fmt.Sprintf("It carries %.0f%% of requests through this gateway", 100*it.RequestShare)
	if len(it.Consumers) > 0 {
		names := make([]string, 0, 3)
		for i, c := range it.Consumers {
			if i == 3 {
				break
			}
			names = append(names, c.Name)
		}
		traffic += " (" + strings.Join(names, ", ") + ")"
	}
	traffic += "."

	wasAtRisk := prev != nil && (prev.Status == core.StatusDeprecated || prev.Status == core.StatusRetired)
	switch {
	case atRisk && !wasAtRisk:
		title := fmt.Sprintf("%s is scheduled for shutdown", f.ID())
		if f.ShutdownDate != "" {
			title += " on " + f.ShutdownDate
		}
		if f.Status == core.StatusRetired {
			title = fmt.Sprintf("%s has been shut down and is still receiving requests", f.ID())
		}
		detail := traffic
		if f.Replacement != "" {
			detail += " The provider names " + f.Replacement + " as the replacement."
		}
		a.Event(core.Event{Kind: "sunset_found", Level: level, Model: f.ID(), Title: title, Detail: detail})
	case atRisk && prev != nil && prev.ShutdownDate != f.ShutdownDate && f.ShutdownDate != "":
		a.Event(core.Event{Kind: "sunset_moved", Level: level, Model: f.ID(),
			Title:  fmt.Sprintf("Shutdown date for %s changed from %s to %s", f.ID(), orUnknown(prev.ShutdownDate), f.ShutdownDate),
			Detail: traffic})
	case !atRisk && wasAtRisk && f.Status != core.StatusUnknown:
		a.Event(core.Event{Kind: "sunset_cleared", Level: "info", Model: f.ID(),
			Title: fmt.Sprintf("No retirement notice found for %s any more", f.ID()), Detail: "Earlier scans found one. Check the evidence before relying on this."})
	}

	// Countdown reminders, once per threshold.
	if atRisk && ex.DaysLeft != nil && *ex.DaysLeft >= 0 {
		for _, th := range []int{30, 14, 7, 1} {
			if *ex.DaysLeft > th {
				continue
			}
			key := fmt.Sprintf("notified:%s:%s:%d", f.ID(), f.ShutdownDate, th)
			var seen bool
			if a.Store.Get(store.Meta, key, &seen) == nil {
				break
			}
			_ = a.Store.Put(store.Meta, key, true)
			if wasAtRisk { // the first sighting already raised an event
				a.Event(core.Event{Kind: "sunset_countdown", Level: level, Model: f.ID(),
					Title: fmt.Sprintf("%d day(s) until %s is shut down", *ex.DaysLeft, f.ID()), Detail: traffic})
			}
			break
		}
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ---------------------------------------------------------------------------
// Events and alerts
// ---------------------------------------------------------------------------

// Event records an activity entry and, for warnings and above, posts it to
// the alert webhook.
func (a *App) Event(e core.Event) {
	e.ID = a.Store.NewID("evt")
	e.At = time.Now().UTC()
	if a.Cfg.AlertWebhook != "" && (e.Level == "warn" || e.Level == "critical") {
		e.Alerted = a.alert(e) == nil
	}
	_ = a.Store.Put(store.Events, e.ID, e)
	_ = a.Store.Trim(store.Events, 500)
	a.Log.Info("event", "kind", e.Kind, "level", e.Level, "model", e.Model, "title", e.Title)
}

// alert posts an event to the webhook. The body carries a "text" field, which
// Slack-compatible incoming webhooks render, plus the structured event.
func (a *App) alert(e core.Event) error {
	icon := map[string]string{"critical": "[CRITICAL]", "warn": "[WARNING]"}[e.Level]
	body, _ := json.Marshal(map[string]any{"text": strings.TrimSpace(icon + " Heimdall: " + e.Title + "\n" + e.Detail), "event": e})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.AlertWebhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.Log.Warn("alert webhook failed", "err", err)
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		a.Log.Warn("alert webhook rejected the event", "status", resp.StatusCode)
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Exposures
// ---------------------------------------------------------------------------

// Exposures joins the inventory with stored findings, migrations and the
// latest rehearsal for each model, most urgent first.
func (a *App) Exposures(ctx context.Context) ([]core.Exposure, *core.Inventory) {
	inv := a.Inventory(ctx, 20*time.Second)
	now := time.Now()
	migs := map[string]core.Migration{}
	if all, err := store.List[core.Migration](a.Store, store.Migrations, false, 0); err == nil {
		for _, m := range all {
			migs[m.Model] = m
		}
	}
	latest := map[string]core.RehearsalRef{}
	if all, err := store.List[core.Rehearsal](a.Store, store.Rehearsals, true, 200); err == nil {
		for _, r := range all {
			if _, ok := latest[r.Baseline]; !ok && r.Status == "done" {
				latest[r.Baseline] = core.RehearsalRef{ID: r.ID, Candidate: r.Candidate, Verdict: r.Verdict, Score: r.Score, At: r.FinishedAt}
			}
		}
	}
	var out []core.Exposure
	seen := map[string]bool{}
	add := func(ref core.ModelRef, t *core.InventoryItem) {
		var f *core.Finding
		var stored core.Finding
		if a.Store.Get(store.Findings, ref.ID(), &stored) == nil {
			f = &stored
		}
		ex := radar.Exposure(ref, f, t, now)
		if m, ok := migs[ref.ID()]; ok && m.Stage != core.StageNone && m.Stage != "" {
			mm := m
			ex.Migration = &mm
		}
		if r, ok := latest[ref.ID()]; ok {
			rr := r
			ex.Rehearsal = &rr
		}
		out = append(out, ex)
		seen[ref.ID()] = true
	}
	if inv != nil {
		for i := range inv.Items {
			add(inv.Items[i].ModelRef, &inv.Items[i])
		}
	}
	// A promoted migration moves a model's traffic away, so the model drops
	// out of the inventory. It still belongs on the board.
	for id, m := range migs {
		if !seen[id] && m.Stage != core.StageNone && m.Stage != "" {
			add(core.ParseModelRef(id), nil)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := radar.RiskRank(out[i].Risk), radar.RiskRank(out[j].Risk)
		if ri != rj {
			return ri < rj
		}
		di, dj := 1<<30, 1<<30
		if out[i].DaysLeft != nil {
			di = *out[i].DaysLeft
		}
		if out[j].DaysLeft != nil {
			dj = *out[j].DaysLeft
		}
		if di != dj {
			return di < dj
		}
		var ti, tj int
		if out[i].Traffic != nil {
			ti = out[i].Traffic.Requests
		}
		if out[j].Traffic != nil {
			tj = out[j].Traffic.Requests
		}
		return ti > tj
	})
	return out, inv
}

// ---------------------------------------------------------------------------
// Background work
// ---------------------------------------------------------------------------

// Start launches the scheduler, the migration health monitor and, in
// simulator mode, the simulated applications. It returns at once.
func (a *App) Start(ctx context.Context) {
	go a.schedule(ctx)
	go a.monitorMigrations(ctx)
	if a.World != nil && a.Cfg.SimTraffic {
		go a.simulateTraffic(ctx)
	}
}

func (a *App) waitForGateway(ctx context.Context) bool {
	for i := 0; i < 120; i++ {
		hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := a.GW.Health(hctx)
		cancel()
		if err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

func (a *App) schedule(ctx context.Context) {
	if !a.waitForGateway(ctx) {
		a.Log.Warn("gateway did not become healthy; scans will run when requested")
		return
	}
	// In simulator mode wait for the first burst of application traffic, so
	// the first scan has an inventory to work on.
	if a.World != nil && a.Cfg.SimTraffic {
		for i := 0; i < 60 && a.traffic.Sent.Load() < 100; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		time.Sleep(1500 * time.Millisecond) // let the gateway flush its log writer
	}
	run := func(trigger string) {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		if scan, err := a.RunScan(sctx, trigger); err != nil {
			a.Log.Warn("scan failed", "err", err)
		} else {
			a.Log.Info("scan finished", "models", scan.Models, "at_risk", scan.AtRisk, "searches", scan.Searches, "llm_calls", scan.LLMCalls)
		}
	}
	run("startup")
	if a.Cfg.ScanInterval <= 0 {
		return
	}
	for {
		a.mu.Lock()
		a.nextScan = time.Now().Add(a.Cfg.ScanInterval)
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.Cfg.ScanInterval):
			run("schedule")
		}
	}
}

// monitorMigrations keeps canary health current and applies the guard.
func (a *App) monitorMigrations(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		migs, err := store.List[core.Migration](a.Store, store.Migrations, false, 0)
		if err != nil {
			continue
		}
		for _, m := range migs {
			if m.Stage == core.StageNone || m.Stage == "" || m.RuleID == "" {
				continue
			}
			hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			h, err := a.cut.Health(hctx, m)
			cancel()
			if err != nil || h == nil {
				continue
			}
			// Re-read: the operator may have changed the stage meanwhile.
			var cur core.Migration
			if a.Store.Get(store.Migrations, m.Model, &cur) != nil || cur.RuleID != m.RuleID || !cur.UpdatedAt.Equal(m.UpdatedAt) {
				continue
			}
			cur.Health = h
			if tripped, note := cutover.Guard(h, 20); tripped && cur.Stage == core.StageCanary && cur.GuardNote == "" {
				cur.GuardNote = note
				if a.Cfg.AutoRollback {
					rctx, rcancel := context.WithTimeout(ctx, 20*time.Second)
					rolled, rerr := a.cut.Rollback(rctx, cur)
					rcancel()
					if rerr == nil {
						rolled.GuardNote = note + " Heimdall rolled the canary back."
						cur = rolled
					}
				}
				a.Event(core.Event{Kind: "canary_guard", Level: "critical", Model: cur.Model,
					Title: fmt.Sprintf("Canary for %s is failing on %s", cur.Model, cur.Replacement), Detail: cur.GuardNote})
			}
			_ = a.Store.Put(store.Migrations, cur.Model, cur)
		}
	}
}

// simulateTraffic plays the simulated applications against the gateway.
func (a *App) simulateTraffic(ctx context.Context) {
	if !a.waitForGateway(ctx) {
		return
	}
	items := sim.DefaultWorkload()
	// Two passes give the busiest application a realistic lead over the rest.
	sim.Send(ctx, a.GW, items, 8, &a.traffic)
	sim.Send(ctx, a.GW, items[:len(items)/2], 8, &a.traffic)
	a.Log.Info("simulated applications sent their first burst", "requests", a.traffic.Sent.Load(), "failed", a.traffic.Failed.Load())
	sim.Trickle(ctx, a.GW, items, 3*time.Second, 40000, &a.traffic)
}
