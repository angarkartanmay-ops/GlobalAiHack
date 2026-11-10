// Package sim is a self-contained stand-in for the two things Heimdall
// cannot reach without credentials: model providers and the web.
//
// It serves an OpenAI-compatible provider API and a Tavily-compatible search
// API over a small fictional world: two invented providers ("Aurora AI" and
// "Lumen Labs"), their documentation pages, and an invented company
// ("Northpost") whose applications send traffic. Every host uses the reserved
// .example domain, so nothing here can be mistaken for a real provider's
// lifecycle data.
//
// A real Bifrost gateway sits between Heimdall and the simulator, exactly as
// it sits between Heimdall and real providers. The simulator exists so that
// the full loop (inventory, radar, rehearsal, cutover) can be tested end to
// end and shown without paid keys. It is not used in live mode.
package sim

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
)

// Simulated providers and the analyst model.
const (
	ProviderAurora = "aurora"
	ProviderLumen  = "lumen"
	AnalystModel   = "aurora/aurora-analyst"

	// DefaultKey is the bearer key the gateway presents to the simulator.
	DefaultKey = "sk-sim-heimdall"
)

// Consumer is a simulated application with its own gateway virtual key.
type Consumer struct {
	ID    string // virtual key id on the gateway
	Name  string
	Value string // virtual key value
}

// Consumers are the applications that send simulated traffic.
var Consumers = []Consumer{
	{"vk-checkout", "checkout-app", "sk-bf-sim-checkout-0000000000000000000000000000000000000001"},
	{"vk-support", "support-bot", "sk-bf-sim-support-00000000000000000000000000000000000000002"},
	{"vk-search", "search-service", "sk-bf-sim-search-000000000000000000000000000000000000000003"},
	{"vk-legacy", "legacy-batch", "sk-bf-sim-legacy-000000000000000000000000000000000000000004"},
}

// HeimdallKey is Heimdall's own virtual key in the simulated gateway config.
const HeimdallKey = "sk-bf-sim-heimdall-0000000000000000000000000000000000000009"

// traits describe how a simulated model behaves.
type traits struct {
	provider       string
	prompt, output float64 // USD per token
	latency        [2]int  // milliseconds, min and max
	style          string  // classic, concise, verbose
	// Quirks fire on order numbers divisible by N (or, for refusals, on a
	// hash of the question), so the same request always behaves the same way.
	jsonFenceEvery    int  // wrap JSON in a markdown fence
	jsonExtraKeyEvery int  // add an unrequested key
	toolIntArgEvery   int  // send a numeric id where a string is expected
	toolSkipEvery     int  // answer in prose instead of calling the tool
	refuseEvery       int  // refuse a harmless question
	dropReturnWindow  bool // leave a required fact out of the returns answer
	facts             map[string]string
	apiShutdown       bool // provider API reports shutdown_date for this model
}

// Facts the simulated web holds as current, and what each model "learned".
var currentFacts = map[string]string{"api": "v5", "surcharge": "$6.50", "ceo": "Priya Raman", "delivery": "2 to 4 business days"}

var models = map[string]traits{
	"aurora-large-1": {provider: ProviderAurora, prompt: 3e-6, output: 12e-6, latency: [2]int{180, 420}, style: "classic",
		facts: map[string]string{"api": "v4", "surcharge": "$4.50", "ceo": "Daniel Okafor", "delivery": "2 to 4 business days"}},
	"aurora-large-2": {provider: ProviderAurora, prompt: 2e-6, output: 8e-6, latency: [2]int{120, 300}, style: "concise",
		jsonFenceEvery: 10, toolIntArgEvery: 12,
		facts: map[string]string{"api": "v5", "surcharge": "$6.50", "ceo": "Priya Raman", "delivery": "2 to 4 business days"}},
	"aurora-mini-1": {provider: ProviderAurora, prompt: 0.4e-6, output: 1.6e-6, latency: [2]int{60, 140}, style: "classic", apiShutdown: true,
		facts: currentFacts},
	"aurora-mini-2": {provider: ProviderAurora, prompt: 0.3e-6, output: 1.2e-6, latency: [2]int{50, 120}, style: "classic",
		facts: currentFacts},
	"aurora-instant-0": {provider: ProviderAurora, prompt: 0.2e-6, output: 0.8e-6, latency: [2]int{40, 90}, style: "classic",
		facts: currentFacts},
	"aurora-analyst": {provider: ProviderAurora, prompt: 1e-6, output: 4e-6, latency: [2]int{80, 160}, style: "classic", facts: currentFacts},
	"lumen-chat-3": {provider: ProviderLumen, prompt: 2.5e-6, output: 10e-6, latency: [2]int{200, 500}, style: "verbose",
		jsonExtraKeyEvery: 3, toolSkipEvery: 4, refuseEvery: 11, dropReturnWindow: true,
		facts: map[string]string{"api": "v5", "surcharge": "$4.50", "ceo": "Daniel Okafor", "delivery": "3 to 5 business days"}},
	"lumen-chat-2-0815": {provider: ProviderLumen, prompt: 2e-6, output: 8e-6, latency: [2]int{220, 520}, style: "classic",
		facts: map[string]string{"api": "v4", "surcharge": "$4.50", "ceo": "Daniel Okafor", "delivery": "2 to 4 business days"}},
}

// ModelIDs returns every simulated model as provider/model, sorted by provider then name.
func ModelIDs() []string {
	order := []string{"aurora-large-1", "aurora-large-2", "aurora-mini-1", "aurora-mini-2", "aurora-instant-0", "aurora-analyst", "lumen-chat-3", "lumen-chat-2-0815"}
	out := make([]string, len(order))
	for i, m := range order {
		out[i] = models[m].provider + "/" + m
	}
	return out
}

// Pricing returns the gateway pricing-file entries for the simulated models.
func Pricing() map[string]map[string]any {
	out := map[string]map[string]any{}
	for name, t := range models {
		out[t.provider+"/"+name] = map[string]any{
			"base_model": name, "provider": t.provider, "mode": "chat",
			"input_cost_per_token": t.prompt, "output_cost_per_token": t.output,
		}
	}
	return out
}

// Page is one document on the simulated web.
type Page struct {
	URL       string
	Title     string
	Text      string
	Published string
}

// World is the simulated state: the clock the pages were written against,
// which models have been shut down, and the pages themselves.
type World struct {
	T0   time.Time
	Key  string
	Fast bool // skip simulated latency (tests)
	// Faults makes the simulated analyst misbehave in two specific, realistic
	// ways so that Heimdall's grounding guard has something to catch.
	Faults bool

	mu      sync.RWMutex
	retired map[string]bool
	pages   []Page
}

// NewWorld builds the world relative to now. All dates on the simulated
// pages are offsets from now, so the scenario reads the same on any day.
func NewWorld(now time.Time) *World {
	w := &World{T0: now.UTC(), Key: DefaultKey, Faults: true, retired: map[string]bool{"aurora-instant-0": true}}
	w.pages = w.buildPages()
	return w
}

// Dates the scenario turns on.
func (w *World) day(offset int) time.Time { return w.T0.AddDate(0, 0, offset) }
func long(t time.Time) string             { return t.Format("January 2, 2006") }
func iso(t time.Time) string              { return t.Format("2006-01-02") }

// LargeShutdown and friends are exported for tests.
func (w *World) LargeShutdown() time.Time   { return w.day(23) }
func (w *World) MiniShutdown() time.Time    { return w.day(96) }
func (w *World) InstantShutdown() time.Time { return w.day(-5) }
func (w *World) LumenTwoMonth() time.Time   { return w.day(130) }
func (w *World) RumouredLarge() time.Time   { return w.day(53) }

// Retire shuts a simulated model down: from now on the provider answers 404.
func (w *World) Retire(model string) bool {
	if _, ok := models[model]; !ok {
		return false
	}
	w.mu.Lock()
	w.retired[model] = true
	w.mu.Unlock()
	return true
}

// Revive brings a simulated model back (used to reset a demo).
func (w *World) Revive(model string) {
	w.mu.Lock()
	delete(w.retired, model)
	w.mu.Unlock()
}

// Retired reports whether a model has been shut down.
func (w *World) Retired(model string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.retired[model]
}

// RetiredModels lists models that are shut down.
func (w *World) RetiredModels() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []string
	for m := range w.retired {
		out = append(out, models[m].provider+"/"+m)
	}
	return out
}

// Registry returns lifecycle sources for the simulated providers.
func Registry() evidence.Registry {
	return evidence.Registry{
		ProviderAurora: {Domains: []string{"docs.aurora-ai.example", "aurora-ai.example"}, Pages: []string{"https://docs.aurora-ai.example/models/deprecations"}},
		ProviderLumen:  {Domains: []string{"docs.lumenlabs.example", "lumenlabs.example"}, Pages: []string{"https://docs.lumenlabs.example/models/lifecycle"}},
	}
}

func (w *World) buildPages() []Page {
	var b strings.Builder
	b.WriteString("# Model deprecations\n\n")
	b.WriteString("Aurora AI retires older models on a published schedule. After a model's shutdown date, requests to it return a 404 `model_not_found` error. Deprecated models keep working until their shutdown date.\n\n")
	b.WriteString("## Deprecation schedule\n\n")
	b.WriteString("| Model | Deprecation announced | Shutdown date | Recommended replacement |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| aurora-large-1 | %s | %s | aurora-large-2 |\n", long(w.day(-67)), long(w.LargeShutdown()))
	fmt.Fprintf(&b, "| aurora-mini-1 | %s | %s | aurora-mini-2 |\n", long(w.day(-24)), long(w.MiniShutdown()))
	fmt.Fprintf(&b, "| aurora-instant-0 | %s | %s | aurora-mini-2 |\n", long(w.day(-125)), long(w.InstantShutdown()))
	b.WriteString("\n## Current models\n\n| Model | Status |\n|---|---|\n")
	b.WriteString("| aurora-large-2 | Generally available. No retirement is scheduled for aurora-large-2. |\n")
	b.WriteString("| aurora-mini-2 | Generally available. No retirement is scheduled for aurora-mini-2. |\n")
	deprecations := b.String()

	changelog := fmt.Sprintf(`# Changelog

## %s

We are announcing the deprecation of aurora-large-1. The model aurora-large-1 will be shut down on %s. We recommend moving to aurora-large-2, which is cheaper per token and has a more recent knowledge cutoff.

## %s

aurora-mini-2 is now generally available. It replaces aurora-mini-1 for high-volume workloads.
`, long(w.day(-67)), long(w.LargeShutdown()), long(w.day(-30)))

	rumour := fmt.Sprintf(`# Aurora Large 1 is on the way out

Developers on Aurora AI's platform have some time to prepare. According to a notice circulated earlier this year, aurora-large-1 is expected to be shut down on %s. Teams running production traffic on it should plan a migration.

ModelWatch is an independent newsletter and is not affiliated with Aurora AI.
`, long(w.RumouredLarge()))

	forum := fmt.Sprintf(`# aurora-mini-1 end of life?

Posted in Aurora AI community > API

Heads up for anyone still on the small model. Support confirmed to me that aurora-mini-1 stops working on %s, and that aurora-mini-2 is the drop-in.

Reply: Thanks, we still have a batch job on it.
`, long(w.MiniShutdown()))

	lumen := fmt.Sprintf(`# Model lifecycle

## Generally available

lumen-chat-3 is our current flagship chat model. No retirement date has been announced for lumen-chat-3.

## Legacy

The lumen-chat-2 family, including every dated snapshot, will be retired in %s. Move workloads to lumen-chat-3 before then.
`, w.LumenTwoMonth().Format("January 2006"))

	apiChangelog := fmt.Sprintf(`# Northpost API changelog

Northpost API v5 is the current version of the Northpost API, released on %s. New integrations should use v5.

Northpost API v4 is deprecated and receives security fixes only.
`, long(w.day(-40)))

	pricing := fmt.Sprintf(`# Northpost pricing

Weekend delivery surcharge: $6.50 per parcel, changed from $4.50 on %s.

Standard parcels up to 2 kg ship at the base rate with no surcharge on weekdays.
`, long(w.day(-20)))

	delivery := `# Delivery times

Standard delivery takes 2 to 4 business days within the country.

Express delivery arrives on the next business day when booked before noon.
`

	leadership := fmt.Sprintf(`# Leadership

Priya Raman was appointed Chief Executive Officer of Northpost in %s, succeeding Daniel Okafor.

The executive team is based at Northpost's headquarters.
`, w.day(-60).Format("January 2006"))

	news := fmt.Sprintf(`# Northpost ships version 5 of its API

Northpost has rolled out v5 of its public API, replacing v4 as the version new integrations should use. The company announced the release on %s.

Shipping News is an independent trade publication.
`, long(w.day(-40)))

	returns := `# Returns

Parcels can be returned within 30 days of delivery. Start a return from the tracking page to receive a prepaid label.
`

	return []Page{
		{"https://docs.aurora-ai.example/models/deprecations", "Model deprecations - Aurora AI Docs", deprecations, iso(w.day(-24))},
		{"https://docs.aurora-ai.example/changelog", "Changelog - Aurora AI Docs", changelog, iso(w.day(-30))},
		{"https://modelwatch.example/blog/aurora-large-1-sunset", "Aurora Large 1 is on the way out - ModelWatch", rumour, iso(w.day(-80))},
		{"https://community.devforum.example/t/aurora-mini-1-end-of-life/4411", "aurora-mini-1 end of life? - DevForum", forum, iso(w.day(-12))},
		{"https://docs.lumenlabs.example/models/lifecycle", "Model lifecycle - Lumen Labs Docs", lumen, iso(w.day(-45))},
		{"https://developers.northpost.example/api/changelog", "API changelog - Northpost Developers", apiChangelog, iso(w.day(-40))},
		{"https://www.northpost.example/pricing", "Pricing - Northpost", pricing, iso(w.day(-20))},
		{"https://www.northpost.example/help/delivery-times", "Delivery times - Northpost Help", delivery, iso(w.day(-200))},
		{"https://www.northpost.example/about/leadership", "Leadership - Northpost", leadership, iso(w.day(-58))},
		{"https://shippingnews.example/northpost-api-v5", "Northpost ships version 5 of its API - Shipping News", news, iso(w.day(-39))},
		{"https://www.northpost.example/help/returns", "Returns - Northpost Help", returns, iso(w.day(-300))},
	}
}

// Pages returns the simulated web.
func (w *World) Pages() []Page { return w.pages }
