package radar

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/analyst"
	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
	"github.com/angarkartanmay-ops/globalaihack/internal/tavily"
)

// TaskSunset is the analyst task name for notice extraction.
const TaskSunset = "sunset_extract"

// SunsetSystemPrompt instructs the analyst model. The model's job is narrow
// on purpose: point at the text that states a notice. It is not trusted with
// the conclusion; Heimdall re-derives dates and status from the quoted text.
const SunsetSystemPrompt = `You read excerpts from AI model providers' documentation and report what they say about the lifecycle of ONE target model.

Rules:
- Use only the excerpts. Do not use what you remember about any model: lifecycles change after your training data ends, and your memory of them is exactly the stale information this check exists to replace.
- Report a notice only when an excerpt states a deprecation, retirement, shutdown or end of life, or states that the model is current with no retirement planned.
- An excerpt about a different model is not a notice for the target, even if the names are similar. "gpt-4o-mini" is not "gpt-4o".

Output one object: {"notices": [...], "reasoning": "one or two sentences"}.
Each notice:
- "passage": the excerpt id it comes from.
- "applies": "exact" if the excerpt names exactly the target identifier; "alias" if it names an alias, display name or dated snapshot that resolves to the same model; "family" if it names another member of the same family; "none" if it is about a different model.
- "status": "deprecated" (retirement announced, still served), "retired" (already shut down), "active" (stated as current), or "unknown".
- "shutdown_date": the date requests stop being served, as YYYY-MM-DD, copied from the excerpt. null if the excerpt gives none. This is not the announcement date.
- "replacement": the replacement model the provider names, written exactly as in the excerpt. null if it names none.
- "quote": the shortest span of the excerpt that supports this notice (one table row, or one or two sentences), copied character for character. If you give a shutdown_date, the quote must contain that date.

If no excerpt says anything about the target model's lifecycle, return {"notices": [], "reasoning": "..."}.`

type notice struct {
	Passage      analyst.Str `json:"passage"`
	Applies      analyst.Str `json:"applies"`
	Status       analyst.Str `json:"status"`
	ShutdownDate analyst.Str `json:"shutdown_date"`
	Replacement  analyst.Str `json:"replacement"`
	Quote        analyst.Str `json:"quote"`
}

type extraction struct {
	Notices   []notice    `json:"notices"`
	Reasoning analyst.Str `json:"reasoning"`
}

// doc is one retrieved source document.
type doc struct {
	url, title, text, via, published string
	official                         bool
}

// excerpt is a passage handed to the analyst.
type excerpt struct {
	id      string
	doc     doc
	text    string
	mention string
}

// Radar checks models against what their providers have published.
type Radar struct {
	GW       Gateway
	Web      tavily.Client
	LLM      analyst.LLM
	Registry evidence.Registry
	Now      func() time.Time
	Log      *slog.Logger

	// MaxExcerpts bounds what one analyst call sees.
	MaxExcerpts int
	// OpenWeb adds a second search outside the provider's own domains. Such
	// sources can corroborate or give early warning, but they can never
	// outrank the provider's documentation or produce high confidence alone.
	OpenWeb bool

	mu     sync.Mutex
	pages  map[string]*pageSet // provider -> lifecycle pages, per scan
	models []bifrost.ModelInfo // gateway model list, per scan
}

type pageSet struct {
	once sync.Once
	docs []doc
	err  error
}

// New returns a radar.
func New(gw Gateway, web tavily.Client, llm analyst.LLM, reg evidence.Registry, log *slog.Logger) *Radar {
	if log == nil {
		log = slog.Default()
	}
	return &Radar{GW: gw, Web: web, LLM: llm, Registry: reg, Now: time.Now, Log: log, MaxExcerpts: 8}
}

// Reset drops per-scan caches so the next check reads sources afresh.
func (r *Radar) Reset() {
	r.mu.Lock()
	r.pages = nil
	r.models = nil
	r.mu.Unlock()
}

// lifecyclePages reads a provider's lifecycle pages once per scan.
func (r *Radar) lifecyclePages(ctx context.Context, provider string) []doc {
	src, ok := r.Registry.For(provider)
	if !ok || len(src.Pages) == 0 || r.Web == nil {
		return nil
	}
	r.mu.Lock()
	if r.pages == nil {
		r.pages = map[string]*pageSet{}
	}
	ps := r.pages[provider]
	if ps == nil {
		ps = &pageSet{}
		r.pages[provider] = ps
	}
	r.mu.Unlock()

	ps.once.Do(func() {
		resp, err := r.Web.Extract(ctx, tavily.ExtractRequest{URLs: src.Pages, ExtractDepth: "advanced", Format: "markdown"})
		if err != nil {
			ps.err = err
			r.Log.Warn("lifecycle page extract failed", "provider", provider, "err", err)
			return
		}
		for _, res := range resp.Results {
			if strings.TrimSpace(res.RawContent) == "" {
				continue
			}
			ps.docs = append(ps.docs, doc{url: res.URL, text: res.RawContent, via: "tavily.extract", official: r.Registry.Official(provider, res.URL)})
		}
		for _, f := range resp.FailedResults {
			r.Log.Info("lifecycle page not extracted", "provider", provider, "url", f.URL, "err", f.Error)
		}
	})
	return ps.docs
}

func (r *Radar) gatewayModels(ctx context.Context) []bifrost.ModelInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.models == nil {
		ms, err := r.GW.ListModels(ctx)
		if err != nil {
			r.Log.Warn("gateway model list failed", "err", err)
			ms = []bifrost.ModelInfo{}
		}
		r.models = ms
	}
	return r.models
}

// search runs the per-model web search, restricted to the provider's own
// documentation when Heimdall knows where that is.
func (r *Radar) search(ctx context.Context, ref core.ModelRef, open bool) ([]doc, error) {
	req := tavily.SearchRequest{
		Query:             fmt.Sprintf("%s %s model deprecation retirement shutdown date", ref.Provider, ref.Model),
		SearchDepth:       "advanced",
		MaxResults:        5,
		IncludeRawContent: "markdown",
	}
	if src, ok := r.Registry.For(ref.Provider); ok {
		if open {
			req.ExcludeDomains = src.Domains
			req.MaxResults = 3
		} else {
			req.IncludeDomains = src.Domains
		}
	}
	resp, err := r.Web.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	var docs []doc
	for _, res := range resp.Results {
		if strings.TrimSpace(res.Text()) == "" {
			continue
		}
		docs = append(docs, doc{url: res.URL, title: res.Title, text: res.Text(), via: "tavily.search",
			published: res.PublishedDate, official: r.Registry.Official(ref.Provider, res.URL)})
	}
	return docs, nil
}

// excerptsFor cuts the retrieved documents down to passages that mention the
// model, best sources first.
func (r *Radar) excerptsFor(docs []doc, model string) []excerpt {
	var all []excerpt
	seen := map[string]bool{}
	for _, d := range docs {
		for _, p := range evidence.Passages(d.text, model, evidence.DefaultPassageOptions) {
			key := evidence.Canon(p.Text)
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, excerpt{doc: d, text: p.Text, mention: p.Mention})
		}
	}
	rank := func(e excerpt) int {
		n := 0
		if !e.doc.official {
			n += 2
		}
		if e.mention != evidence.MentionExact {
			n++
		}
		return n
	}
	sort.SliceStable(all, func(i, j int) bool { return rank(all[i]) < rank(all[j]) })
	if len(all) > r.MaxExcerpts {
		all = all[:r.MaxExcerpts]
	}
	for i := range all {
		all[i].id = fmt.Sprintf("p%d", i+1)
	}
	return all
}

// FormatExcerpts renders excerpts for the analyst prompt.
func formatExcerpts(ex []excerpt) string {
	var b strings.Builder
	for _, e := range ex {
		fmt.Fprintf(&b, "<excerpt id=%q url=%q mention=%q>\n%s\n</excerpt>\n\n", e.id, e.doc.url, e.mention, e.text)
	}
	return b.String()
}

// Check investigates one model and returns a finding. prev, when given, is
// the previous finding for the same model; differences are recorded as changes.
func (r *Radar) Check(ctx context.Context, ref core.ModelRef, prev *core.Finding, run string) core.Finding {
	now := r.Now().UTC()
	f := core.Finding{ModelRef: ref, Status: core.StatusUnknown, Confidence: core.ConfidenceNone,
		FirstSeen: now, LastVerified: now, Evidence: []core.Evidence{}, Inference: []string{}}
	if prev != nil {
		f.FirstSeen = prev.FirstSeen
		f.Changes = prev.Changes
	}

	// 1. First-party signal: what the provider's own API says, via the gateway.
	if info, err := r.GW.RetrieveModel(ctx, ref.ID()); err == nil && info != nil {
		if iso := evidence.NormalizeISO(info.ShutdownDate); iso != "" {
			f.Evidence = append(f.Evidence, core.Evidence{
				ID: "g1", Kind: "gateway", Via: "bifrost.models",
				Title:    "Provider model metadata, read through Bifrost GET /v1/models/" + ref.ID(),
				Quote:    fmt.Sprintf(`"shutdown_date": %q`, info.ShutdownDate),
				Grounded: true, Official: true, Applies: "exact",
				Status: statusFor(core.StatusDeprecated, iso, now), ShutdownDate: iso, DatePrecision: "day", RetrievedAt: now,
			})
		}
	} else if err != nil && bifrost.IsStatus(err, 404) {
		f.Inference = append(f.Inference, "The provider's API returned 404 for this model's metadata through the gateway. That is consistent with a retired model, but on its own it is not proof.")
	}

	// 2. Web signal: lifecycle pages plus a targeted search.
	var docs []doc
	var webErr error
	if r.Web != nil {
		docs = append(docs, r.lifecyclePages(ctx, ref.Provider)...)
		found, err := r.search(ctx, ref, false)
		if err != nil {
			webErr = err
			r.Log.Warn("search failed", "model", ref.ID(), "err", err)
		}
		docs = append(docs, found...)
		if _, known := r.Registry.For(ref.Provider); known && r.OpenWeb && err == nil {
			if more, err := r.search(ctx, ref, true); err == nil {
				docs = append(docs, more...)
			}
		}
	}
	ex := r.excerptsFor(docs, ref.Model)

	// 3. Analyst reads the excerpts; 4. Heimdall verifies every quote.
	if len(ex) > 0 && r.LLM != nil {
		var out extraction
		user := fmt.Sprintf("Target model: %s\nProvider: %s\nToday: %s\n\n%s", ref.Model, ref.Provider, now.Format("2006-01-02"), formatExcerpts(ex))
		if err := r.LLM.JSON(ctx, TaskSunset, SunsetSystemPrompt, user, &out, run); err != nil {
			f.Error = err.Error()
			r.Log.Warn("analyst failed", "model", ref.ID(), "err", err)
		} else {
			byID := map[string]excerpt{}
			for _, e := range ex {
				byID[e.id] = e
			}
			for i, n := range out.Notices {
				ev, ok, note := r.ground(n, byID, ref, now)
				if note != "" {
					f.Inference = append(f.Inference, note)
				}
				if !ok {
					f.Discarded++
					continue
				}
				ev.ID = fmt.Sprintf("w%d", i+1)
				f.Evidence = append(f.Evidence, ev)
			}
			if s := strings.TrimSpace(string(out.Reasoning)); s != "" {
				f.Inference = append(f.Inference, "Analyst model's reading of the excerpts: "+s)
			}
		}
	}
	if webErr != nil && len(ex) == 0 {
		f.Error = webErr.Error()
	}

	r.conclude(&f, now, len(docs), len(ex))
	r.resolveReplacement(ctx, &f)
	recordChanges(&f, prev, now)
	return f
}

// statusFor derives the status from the shutdown date rather than trusting
// the analyst's wording: a date in the past means retired.
func statusFor(stated, iso string, now time.Time) string {
	if iso == "" {
		return stated
	}
	if days, ok := evidence.DaysUntil(iso, now); ok {
		if days <= 0 {
			return core.StatusRetired
		}
		return core.StatusDeprecated
	}
	return stated
}

// ground turns an analyst notice into evidence, or rejects it. A notice is
// kept only if its quote is found verbatim in the excerpt it cites. A date is
// kept only if it can be parsed out of that quote.
func (r *Radar) ground(n notice, byID map[string]excerpt, ref core.ModelRef, now time.Time) (core.Evidence, bool, string) {
	e, ok := byID[strings.TrimSpace(string(n.Passage))]
	if !ok {
		return core.Evidence{}, false, ""
	}
	applies := strings.ToLower(string(n.Applies))
	if applies != "exact" && applies != "alias" && applies != "family" {
		return core.Evidence{}, false, ""
	}
	quote := strings.TrimSpace(string(n.Quote))
	if !evidence.Grounded(quote, e.text) {
		return core.Evidence{}, false, fmt.Sprintf("Discarded one analyst notice citing %s: its quote does not appear in the retrieved text.", e.doc.url)
	}
	// The analyst may not claim a stronger match than the text supports.
	if applies == "exact" && evidence.Mention(quote, ref.Model) != evidence.MentionExact && e.mention != evidence.MentionExact {
		applies = "alias"
	}
	status := strings.ToLower(string(n.Status))
	switch status {
	case core.StatusDeprecated, core.StatusRetired, core.StatusActive:
	default:
		status = core.StatusUnknown
	}
	ev := core.Evidence{
		Kind: "web", Via: e.doc.via, URL: e.doc.url, Domain: evidence.DomainOf(e.doc.url), Title: e.doc.title,
		Quote: quote, Grounded: true, Official: e.doc.official, Applies: applies, Status: status,
		Replacement: strings.TrimSpace(string(n.Replacement)), PublishedDate: e.doc.published, RetrievedAt: now,
	}
	note := ""
	if iso := evidence.NormalizeISO(string(n.ShutdownDate)); iso != "" {
		if d, found := evidence.DateIn(quote, iso); found {
			ev.ShutdownDate = iso
			ev.DatePrecision = d.Precision
			if d.Precision == "month" {
				ev.ShutdownDate = d.ISO
			}
			ev.Status = statusFor(status, ev.ShutdownDate, now)
		} else {
			note = fmt.Sprintf("The analyst gave %s as the shutdown date for a notice on %s, but that date is not in the quoted text, so Heimdall did not use it.", iso, e.doc.url)
		}
	}
	// A replacement must also be in the quote or the excerpt to count as stated.
	if ev.Replacement != "" && !strings.Contains(evidence.Canon(e.text), evidence.Canon(ev.Replacement)) {
		ev.Replacement = ""
	}
	return ev, true, note
}

// conclude derives status, date, confidence and conflicts from the evidence.
func (r *Radar) conclude(f *core.Finding, now time.Time, docs, excerpts int) {
	var direct, related []core.Evidence
	for _, e := range f.Evidence {
		if e.Applies == "exact" || e.Applies == "alias" {
			direct = append(direct, e)
		} else {
			related = append(related, e)
		}
	}
	var notices, actives []core.Evidence
	for _, e := range direct {
		switch e.Status {
		case core.StatusDeprecated, core.StatusRetired:
			notices = append(notices, e)
		case core.StatusActive:
			actives = append(actives, e)
		}
	}

	if len(notices) == 0 {
		switch {
		case len(actives) > 0:
			f.Status = core.StatusActive
			f.Confidence = core.ConfidenceMedium
			if !anyOfficial(actives) {
				f.Confidence = core.ConfidenceLow
			}
		case f.Error != "" && excerpts == 0:
			f.Status = core.StatusUnknown
		default:
			f.Status = core.StatusNoNotice
			f.Inference = append(f.Inference, fmt.Sprintf(
				"Read %d source document(s) and found no retirement notice that names this model. Absence of a notice is not a guarantee: Heimdall checks again on every scan.", docs))
		}
		if len(related) > 0 {
			f.Inference = append(f.Inference, "A related model in the same family has a lifecycle notice (listed as evidence). It does not name this model, so it does not change this model's status.")
		}
		return
	}

	// Official sources (the provider's API and documentation) decide the date.
	// Third-party sources can only fill in when no official source has one.
	dated := func(es []core.Evidence, official bool) []string {
		set := map[string]bool{}
		for _, e := range es {
			if e.ShutdownDate != "" && e.Official == official {
				set[e.ShutdownDate] = true
			}
		}
		var out []string
		for d := range set {
			out = append(out, d)
		}
		sort.Strings(out)
		return out
	}
	offDates, tpDates := dated(notices, true), dated(notices, false)

	switch {
	case len(offDates) > 0:
		f.ShutdownDate = offDates[0] // earliest: plan for the conservative date
		if len(offDates) > 1 {
			f.Conflict = &core.Conflict{Dates: offDates, Summary: "Official sources give different shutdown dates. Heimdall plans for the earliest."}
		} else if len(tpDates) > 0 && !(len(tpDates) == 1 && tpDates[0] == offDates[0]) {
			f.Conflict = &core.Conflict{Dates: append(append([]string{}, offDates...), tpDates...),
				Summary: "A third-party source gives a different date from the provider's own documentation. Heimdall uses the provider's date."}
		}
	case len(tpDates) > 0:
		f.ShutdownDate = tpDates[0]
		f.Inference = append(f.Inference, "The shutdown date comes from a third-party source only. No official source confirming it was retrieved.")
		if len(tpDates) > 1 {
			f.Conflict = &core.Conflict{Dates: tpDates, Summary: "Third-party sources give different shutdown dates. Heimdall plans for the earliest."}
		}
	}

	// The evidence that carries the chosen date leads; it also supplies precision and replacement.
	var lead *core.Evidence
	for i := range notices {
		e := &notices[i]
		if f.ShutdownDate != "" && e.ShutdownDate != f.ShutdownDate {
			continue
		}
		if lead == nil || evidenceRank(*e) < evidenceRank(*lead) {
			lead = e
		}
	}
	if lead == nil {
		lead = &notices[0]
	}
	f.DatePrecision = lead.DatePrecision
	f.Status = statusFor(lead.Status, f.ShutdownDate, now)
	for _, e := range append([]core.Evidence{*lead}, notices...) {
		if e.Replacement != "" {
			f.Replacement = e.Replacement
			f.ReplacementBasis = "stated"
			break
		}
	}

	// Confidence follows from where the evidence came from, never from how
	// sure the analyst model sounded.
	switch {
	case lead.Official && lead.Applies == "exact" && f.ShutdownDate != "" && lead.DatePrecision == "day":
		f.Confidence = core.ConfidenceHigh
	case lead.Official:
		f.Confidence = core.ConfidenceMedium
	case independentDomains(notices, f.ShutdownDate) >= 2:
		f.Confidence = core.ConfidenceMedium
	default:
		f.Confidence = core.ConfidenceLow
	}
	if f.Conflict != nil && len(offDates) > 1 && f.Confidence == core.ConfidenceHigh {
		f.Confidence = core.ConfidenceMedium
	}
	if f.DatePrecision == "month" {
		f.Inference = append(f.Inference, "The source gives only a month. Heimdall plans for the first day of that month.")
	}
	if lead.Applies == "alias" {
		f.Inference = append(f.Inference, "The notice names an alias or snapshot of this model rather than this exact identifier. Confirm which snapshot your alias resolves to.")
	}
}

func anyOfficial(es []core.Evidence) bool {
	for _, e := range es {
		if e.Official {
			return true
		}
	}
	return false
}

// evidenceRank orders evidence, strongest first.
func evidenceRank(e core.Evidence) int {
	n := 0
	if e.Kind != "gateway" {
		n++
	}
	if !e.Official {
		n += 4
	}
	if e.Applies != "exact" {
		n += 2
	}
	return n
}

func independentDomains(es []core.Evidence, date string) int {
	set := map[string]bool{}
	for _, e := range es {
		if e.ShutdownDate == date && e.Domain != "" {
			set[e.Domain] = true
		}
	}
	return len(set)
}

// resolveReplacement maps the replacement a notice names onto a model the
// gateway can actually route to.
func (r *Radar) resolveReplacement(ctx context.Context, f *core.Finding) {
	if f.Replacement == "" {
		return
	}
	want := evidence.Canon(f.Replacement)
	var sameProvider, anyProvider string
	for _, m := range r.gatewayModels(ctx) {
		ref := core.ParseModelRef(m.ID)
		if evidence.Canon(ref.Model) != want && evidence.Canon(m.ID) != want {
			continue
		}
		if ref.Provider == f.Provider {
			sameProvider = m.ID
			break
		}
		if anyProvider == "" {
			anyProvider = m.ID
		}
	}
	switch {
	case sameProvider != "":
		f.Replacement, f.ReplacementOnGW = sameProvider, true
	case anyProvider != "":
		f.Replacement, f.ReplacementOnGW = anyProvider, true
	default:
		if !strings.Contains(f.Replacement, "/") {
			f.Replacement = f.Provider + "/" + f.Replacement
		}
		f.Inference = append(f.Inference, "The replacement named in the notice is not among the models this gateway currently serves for Heimdall's virtual key.")
	}
}

func recordChanges(f, prev *core.Finding, now time.Time) {
	if prev == nil {
		return
	}
	add := func(field, from, to string) {
		if from != to {
			f.Changes = append(f.Changes, core.Change{At: now, Field: field, From: from, To: to})
		}
	}
	// A failed check must not look like the notice was withdrawn.
	if f.Status == core.StatusUnknown && prev.Status != core.StatusUnknown {
		return
	}
	add("status", prev.Status, f.Status)
	add("shutdown_date", prev.ShutdownDate, f.ShutdownDate)
	add("replacement", prev.Replacement, f.Replacement)
	if len(f.Changes) > 20 {
		f.Changes = f.Changes[len(f.Changes)-20:]
	}
}

// Exposure joins a finding with traffic.
func Exposure(ref core.ModelRef, f *core.Finding, t *core.InventoryItem, now time.Time) core.Exposure {
	ex := core.Exposure{ModelRef: ref, Finding: f, Traffic: t}
	if f != nil && f.ShutdownDate != "" {
		if d, ok := evidence.DaysUntil(f.ShutdownDate, now); ok {
			ex.DaysLeft = &d
		}
	}
	ex.Risk = Risk(f, ex.DaysLeft, t != nil && t.Requests > 0)
	return ex
}
