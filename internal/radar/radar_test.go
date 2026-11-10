package radar

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
	"github.com/angarkartanmay-ops/globalaihack/internal/tavily"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

const officialURL = "https://docs.acme.example/deprecations"

const officialPage = `# Deprecations

| Model | Shutdown date | Replacement |
|---|---|---|
| acme-large-1 | December 3, 2026 | acme-large-2 |
| acme-large-1-mini | January 20, 2027 | acme-mini-2 |
| acme-old-0 | March 1, 2026 | acme-large-2 |
`

// stubGW serves the model list and per-model metadata.
type stubGW struct {
	shutdown map[string]string
	models   []string
}

func (s stubGW) Rankings(context.Context, bifrost.LogFilter) ([]bifrost.Ranking, error) {
	return nil, nil
}
func (s stubGW) RankingsBy(context.Context, string, bifrost.LogFilter) ([]bifrost.DimensionRanking, error) {
	return nil, nil
}
func (s stubGW) Logs(context.Context, bifrost.LogFilter) (*bifrost.LogPage, error) {
	return &bifrost.LogPage{}, nil
}
func (s stubGW) ListModels(context.Context) ([]bifrost.ModelInfo, error) {
	var out []bifrost.ModelInfo
	for _, m := range s.models {
		out = append(out, bifrost.ModelInfo{ID: m})
	}
	return out, nil
}
func (s stubGW) RetrieveModel(_ context.Context, id string) (*bifrost.ModelInfo, error) {
	return &bifrost.ModelInfo{ID: id, ShutdownDate: s.shutdown[id]}, nil
}

// stubWeb returns fixed pages by URL, and search results by restriction.
type stubWeb struct {
	pages      map[string]string
	thirdParty []tavily.SearchResult
	err        error
}

func (s stubWeb) Mode() string           { return "stub" }
func (s stubWeb) Counts() (int64, int64) { return 0, 0 }
func (s stubWeb) Extract(_ context.Context, req tavily.ExtractRequest) (*tavily.ExtractResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out tavily.ExtractResponse
	for _, u := range req.URLs {
		if p, ok := s.pages[u]; ok {
			out.Results = append(out.Results, tavily.ExtractResult{URL: u, RawContent: p})
		}
	}
	return &out, nil
}
func (s stubWeb) Search(_ context.Context, req tavily.SearchRequest) (*tavily.SearchResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(req.ExcludeDomains) > 0 {
		return &tavily.SearchResponse{Results: s.thirdParty}, nil
	}
	return &tavily.SearchResponse{}, nil
}

// scriptedLLM returns a fixed extraction, whatever it is shown. It stands in
// for a model that may quote correctly, misquote, or invent.
type scriptedLLM struct {
	notices []map[string]any
	err     error
	prompts []string
}

func (s *scriptedLLM) Calls() int64      { return int64(len(s.prompts)) }
func (s *scriptedLLM) ModelName() string { return "scripted" }
func (s *scriptedLLM) Text(context.Context, string, string, string, string) (string, error) {
	return "", nil
}
func (s *scriptedLLM) JSON(_ context.Context, _, _, user string, out any, _ string) error {
	s.prompts = append(s.prompts, user)
	if s.err != nil {
		return s.err
	}
	b, _ := json.Marshal(map[string]any{"notices": s.notices, "reasoning": "scripted"})
	return json.Unmarshal(b, out)
}

func newRadar(web tavily.Client, llm *scriptedLLM, gw stubGW) *Radar {
	reg := evidence.Registry{"acme": {Domains: []string{"docs.acme.example"}, Pages: []string{officialURL}}}
	r := New(gw, web, llm, reg, nil)
	r.Now = func() time.Time { return now }
	r.OpenWeb = true
	return r
}

var (
	ref     = core.ModelRef{Provider: "acme", Model: "acme-large-1"}
	web     = stubWeb{pages: map[string]string{officialURL: officialPage}}
	gateway = stubGW{models: []string{"acme/acme-large-1", "acme/acme-large-2"}}
	goodRow = "| acme-large-1 | December 3, 2026 | acme-large-2 |"
)

func mkNotice(quote, date string) map[string]any {
	return map[string]any{"passage": "p1", "applies": "exact", "status": "deprecated", "shutdown_date": date, "replacement": "acme-large-2", "quote": quote}
}

func TestGroundedNoticeBecomesHighConfidenceFinding(t *testing.T) {
	llm := &scriptedLLM{notices: []map[string]any{mkNotice(goodRow, "2026-12-03")}}
	f := newRadar(web, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.Status != core.StatusDeprecated || f.ShutdownDate != "2026-12-03" || f.Confidence != core.ConfidenceHigh {
		t.Fatalf("finding = %s %s %s", f.Status, f.ShutdownDate, f.Confidence)
	}
	if f.Replacement != "acme/acme-large-2" || !f.ReplacementOnGW || f.ReplacementBasis != "stated" {
		t.Errorf("replacement = %q on_gateway=%v basis=%q", f.Replacement, f.ReplacementOnGW, f.ReplacementBasis)
	}
	if len(f.Evidence) != 1 || !f.Evidence[0].Grounded || !f.Evidence[0].Official || f.Evidence[0].URL != officialURL {
		t.Errorf("evidence = %+v", f.Evidence)
	}
	// the analyst is shown only the target's row, never the neighbours'
	if p := llm.prompts[0]; strings.Contains(p, "acme-old-0") || !strings.Contains(p, "| Model | Shutdown date | Replacement |") {
		t.Errorf("analyst prompt should carry the target row and table header only:\n%s", p)
	}
}

func TestInventedQuoteIsDiscarded(t *testing.T) {
	llm := &scriptedLLM{notices: []map[string]any{mkNotice("acme-large-1 will be shut down on December 3, 2026.", "2026-12-03")}}
	f := newRadar(web, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.Status != core.StatusNoNotice || f.ShutdownDate != "" || len(f.Evidence) != 0 || f.Discarded != 1 {
		t.Fatalf("an ungrounded quote must not produce a finding: %s %q evidence=%d discarded=%d", f.Status, f.ShutdownDate, len(f.Evidence), f.Discarded)
	}
	if !strings.Contains(strings.Join(f.Inference, " "), "does not appear in the retrieved text") {
		t.Errorf("the discard should be explained: %v", f.Inference)
	}
}

func TestInventedDateIsNotUsed(t *testing.T) {
	// The quote is real but the analyst reports a date the quote does not contain.
	llm := &scriptedLLM{notices: []map[string]any{mkNotice(goodRow, "2026-11-20")}}
	f := newRadar(web, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.ShutdownDate != "" {
		t.Fatalf("a date that is not in the quote must not be used, got %s", f.ShutdownDate)
	}
	if f.Status != core.StatusDeprecated || f.Confidence == core.ConfidenceHigh {
		t.Errorf("status=%s confidence=%s; a dateless notice cannot be high confidence", f.Status, f.Confidence)
	}
	if !strings.Contains(strings.Join(f.Inference, " "), "not in the quoted text") {
		t.Errorf("the dropped date should be explained: %v", f.Inference)
	}
}

func TestNeighbouringModelsRowCannotBeUsed(t *testing.T) {
	// The analyst points at the similarly named model's row. That row was
	// never part of the excerpt, so the quote cannot be grounded.
	llm := &scriptedLLM{notices: []map[string]any{mkNotice("| acme-large-1-mini | January 20, 2027 | acme-mini-2 |", "2027-01-20")}}
	f := newRadar(web, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.Status != core.StatusNoNotice || f.Discarded != 1 {
		t.Fatalf("status=%s discarded=%d", f.Status, f.Discarded)
	}
}

func TestPastDateMeansRetired(t *testing.T) {
	old := core.ModelRef{Provider: "acme", Model: "acme-old-0"}
	n := mkNotice("| acme-old-0 | March 1, 2026 | acme-large-2 |", "2026-03-01")
	n["status"] = "deprecated" // the analyst's wording is overridden by the date
	f := newRadar(web, &scriptedLLM{notices: []map[string]any{n}}, gateway).Check(context.Background(), old, nil, "t")
	if f.Status != core.StatusRetired {
		t.Fatalf("a shutdown date in the past means retired, got %s", f.Status)
	}
	ex := Exposure(old, &f, &core.InventoryItem{Requests: 4}, now)
	if ex.Risk != core.RiskCritical || ex.DaysLeft == nil || *ex.DaysLeft >= 0 {
		t.Errorf("a retired model with traffic is critical: %+v", ex)
	}
}

func TestThirdPartyCannotOutrankOfficial(t *testing.T) {
	blog := tavily.SearchResult{URL: "https://blog.example/acme", Title: "Blog", RawContent: "Rumour: acme-large-1 is expected to be shut down on January 2, 2027."}
	w := stubWeb{pages: web.pages, thirdParty: []tavily.SearchResult{blog}}
	llm := &scriptedLLM{notices: []map[string]any{
		mkNotice(goodRow, "2026-12-03"),
		{"passage": "p2", "applies": "exact", "status": "deprecated", "shutdown_date": "2027-01-02", "replacement": nil,
			"quote": "acme-large-1 is expected to be shut down on January 2, 2027."},
	}}
	f := newRadar(w, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.ShutdownDate != "2026-12-03" || f.Confidence != core.ConfidenceHigh {
		t.Fatalf("the provider's date must win: %s %s", f.ShutdownDate, f.Confidence)
	}
	if f.Conflict == nil || len(f.Conflict.Dates) != 2 {
		t.Errorf("the disagreement should be reported: %+v", f.Conflict)
	}

	// With only the blog, the date is usable but never high confidence.
	only := stubWeb{pages: map[string]string{}, thirdParty: []tavily.SearchResult{blog}}
	llm = &scriptedLLM{notices: []map[string]any{{"passage": "p1", "applies": "exact", "status": "deprecated", "shutdown_date": "2027-01-02",
		"quote": "acme-large-1 is expected to be shut down on January 2, 2027."}}}
	f = newRadar(only, llm, gateway).Check(context.Background(), ref, nil, "t")
	if f.ShutdownDate != "2027-01-02" || f.Confidence != core.ConfidenceLow {
		t.Errorf("third-party only: %s %s", f.ShutdownDate, f.Confidence)
	}
}

func TestGatewaySignalAloneIsEnough(t *testing.T) {
	gw := stubGW{shutdown: map[string]string{"acme/acme-large-1": "2026-12-15"}, models: gateway.models}
	f := newRadar(stubWeb{}, &scriptedLLM{}, gw).Check(context.Background(), ref, nil, "t")
	if f.Status != core.StatusDeprecated || f.ShutdownDate != "2026-12-15" || f.Confidence != core.ConfidenceHigh {
		t.Fatalf("finding = %s %s %s", f.Status, f.ShutdownDate, f.Confidence)
	}
	if f.Evidence[0].Kind != "gateway" {
		t.Errorf("evidence should be the gateway's own metadata: %+v", f.Evidence[0])
	}
}

func TestOfficialSourcesDisagreeing(t *testing.T) {
	gw := stubGW{shutdown: map[string]string{"acme/acme-large-1": "2026-12-15"}, models: gateway.models}
	llm := &scriptedLLM{notices: []map[string]any{mkNotice(goodRow, "2026-12-03")}}
	f := newRadar(web, llm, gw).Check(context.Background(), ref, nil, "t")
	if f.ShutdownDate != "2026-12-03" || f.Conflict == nil || f.Confidence != core.ConfidenceMedium {
		t.Fatalf("plan for the earliest official date and lower confidence: %s %+v %s", f.ShutdownDate, f.Conflict, f.Confidence)
	}
}

func TestSearchFailureIsUnknownNotSafe(t *testing.T) {
	f := newRadar(stubWeb{err: errors.New("tavily 432 (Tavily plan or credit limit reached)")}, &scriptedLLM{}, gateway).Check(context.Background(), ref, nil, "t")
	if f.Status != core.StatusUnknown || f.Error == "" {
		t.Fatalf("a failed check must be unknown with the error kept, got %s %q", f.Status, f.Error)
	}
}

func TestChangesAreRecorded(t *testing.T) {
	prev := core.Finding{ModelRef: ref, Status: core.StatusDeprecated, ShutdownDate: "2027-01-15", FirstSeen: now.AddDate(0, 0, -9)}
	llm := &scriptedLLM{notices: []map[string]any{mkNotice(goodRow, "2026-12-03")}}
	f := newRadar(web, llm, gateway).Check(context.Background(), ref, &prev, "t")
	var moved bool
	for _, c := range f.Changes {
		if c.Field == "shutdown_date" && c.From == "2027-01-15" && c.To == "2026-12-03" {
			moved = true
		}
	}
	if !moved || !f.FirstSeen.Equal(prev.FirstSeen) {
		t.Errorf("a moved date should be recorded and first-seen kept: %+v", f.Changes)
	}
}

func TestRisk(t *testing.T) {
	d := func(n int) *int { return &n }
	dep := &core.Finding{Status: core.StatusDeprecated}
	cases := []struct {
		days *int
		want string
	}{{d(3), core.RiskCritical}, {d(14), core.RiskCritical}, {d(30), core.RiskHigh}, {d(90), core.RiskMedium}, {d(300), core.RiskLow}, {nil, core.RiskMedium}}
	for _, c := range cases {
		if got := Risk(dep, c.days, true); got != c.want {
			t.Errorf("Risk(days=%v) = %s, want %s", c.days, got, c.want)
		}
	}
	if Risk(&core.Finding{Status: core.StatusActive}, nil, true) != core.RiskNone || Risk(nil, nil, true) != core.RiskNone {
		t.Error("active or unchecked models carry no risk level")
	}
}
