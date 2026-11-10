// Package freshness is the stale-answer detector: it takes an answer a model
// gave, finds the claims in it that can go out of date, checks each against
// what the web says today, and reports which still hold.
//
// The same rules as the radar apply. Tavily supplies the evidence, a model
// served through Bifrost reads it, and a verdict stands only on a quote found
// verbatim in a retrieved source. Evidence and the model's reasoning are kept
// in separate fields all the way to the UI.
package freshness

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/analyst"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
	"github.com/angarkartanmay-ops/globalaihack/internal/tavily"
)

// Analyst task names.
const (
	TaskClaims  = "claims_extract"
	TaskAssess  = "claim_assess"
	TaskRewrite = "answer_rewrite"
)

// ClaimsSystemPrompt asks for the claims in an answer.
const ClaimsSystemPrompt = `You split an AI assistant's answer into atomic factual claims and mark the ones that can go out of date.

A claim is time-sensitive when whether it is true depends on when it is read: the latest version or release of something, a price, who holds a role, whether a product or service is currently available, a ranking or record, a statistic "as of" some time, a law or policy in force, a schedule or deadline. Definitions, mathematics, settled history and how-to instructions are not time-sensitive.

Output one object: {"claims": [...]}.
Each claim:
- "id": "c1", "c2", ...
- "text": the claim as one standalone sentence. Resolve pronouns and references using the question and the answer, so the sentence makes sense alone.
- "time_sensitive": true or false.
- "category": one of "version", "price", "role", "status", "statistic", "policy", "schedule", "other".
- "query": for a time-sensitive claim, a web search query that would find the current fact. Omit for others.

Give at most 12 claims. If there are more, keep the time-sensitive ones.`

// AssessSystemPrompt asks whether one claim still holds.
const AssessSystemPrompt = `You compare ONE claim with web search results retrieved today, and decide whether the claim is still true.

Rules:
- Use only the results. Do not use what you remember: your memory is the possibly-stale information being checked.
- Prefer the most recent and most authoritative result when results disagree.

Output one object:
{"verdict": "supported" | "changed" | "unverifiable",
 "current_value": "the fact as the results state it now, as briefly as possible (a version, a name, a number, a date), or null",
 "source": "the id of the result you rely on, or null",
 "quote": "the shortest span of that result that shows it, copied character for character, or null",
 "reasoning": "one sentence"}

- "supported": a result states the same fact as the claim.
- "changed": a result states a different current fact. Give current_value.
- "unverifiable": the results do not settle it. Do not guess.`

// RewriteSystemPrompt asks for a corrected answer.
const RewriteSystemPrompt = `You correct an AI assistant's answer using verified findings.

Rewrite the answer so it is accurate as of today. Change only the facts the findings show have changed; keep everything else, including tone and structure. After each corrected fact add a citation marker like [1]. Where a finding is "unverifiable", keep the original statement and add "(could not be verified today)". End with a "Sources:" list mapping each marker to its URL. Output the rewritten answer only.`

type claimOut struct {
	ID            analyst.Str  `json:"id"`
	Text          analyst.Str  `json:"text"`
	TimeSensitive analyst.Bool `json:"time_sensitive"`
	Category      analyst.Str  `json:"category"`
	Query         analyst.Str  `json:"query"`
}

type assessOut struct {
	Verdict      analyst.Str `json:"verdict"`
	CurrentValue analyst.Str `json:"current_value"`
	Source       analyst.Str `json:"source"`
	Quote        analyst.Str `json:"quote"`
	Reasoning    analyst.Str `json:"reasoning"`
}

// Checker runs freshness checks.
type Checker struct {
	Web        tavily.Client
	LLM        analyst.LLM
	Now        func() time.Time
	Log        *slog.Logger
	MaxClaims  int // time-sensitive claims checked per answer
	MaxResults int
}

// New returns a checker with default limits.
func New(web tavily.Client, llm analyst.LLM, log *slog.Logger) *Checker {
	if log == nil {
		log = slog.Default()
	}
	return &Checker{Web: web, LLM: llm, Now: time.Now, Log: log, MaxClaims: 6, MaxResults: 5}
}

// Request is one answer to check.
type Request struct {
	Question string
	Answer   string
	Model    string // the model that produced the answer, if known
	Source   string // manual, mcp, rehearsal
	Rewrite  bool
	Run      string
}

// Result is one retrieved search result, as shown to the analyst.
type Result struct {
	ID        string
	URL       string
	Title     string
	Text      string
	Published string
}

// Search retrieves evidence for a query.
func (c *Checker) Search(ctx context.Context, query string) ([]Result, error) {
	resp, err := c.Web.Search(ctx, tavily.SearchRequest{Query: query, SearchDepth: "advanced", MaxResults: c.MaxResults})
	if err != nil {
		return nil, err
	}
	var out []Result
	for i, r := range resp.Results {
		if strings.TrimSpace(r.Content) == "" && strings.TrimSpace(r.RawContent) == "" {
			continue
		}
		text := r.Content
		if strings.TrimSpace(text) == "" {
			text = r.RawContent
		}
		if len(text) > 2400 {
			text = text[:2400]
		}
		out = append(out, Result{ID: fmt.Sprintf("e%d", i+1), URL: r.URL, Title: r.Title, Text: text, Published: r.PublishedDate})
	}
	return out, nil
}

func formatResults(rs []Result) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "<result id=%q url=%q published=%q title=%q>\n%s\n</result>\n\n", r.ID, r.URL, r.Published, r.Title, r.Text)
	}
	return b.String()
}

// Assessment is the outcome of checking one claim against retrieved results.
type Assessment struct {
	Verdict      string
	CurrentValue string
	Confidence   string
	Evidence     []core.Evidence
	Inference    string
}

// Assess decides whether a claim holds, given results already retrieved. The
// analyst's verdict is accepted only when its quote is found verbatim in the
// result it cites; otherwise the claim is reported as unverifiable.
func (c *Checker) Assess(ctx context.Context, claim string, results []Result, run string) Assessment {
	now := c.Now().UTC()
	if len(results) == 0 {
		return Assessment{Verdict: core.VerdictUnverifiable, Confidence: core.ConfidenceNone, Inference: "The search returned no usable results."}
	}
	var out assessOut
	user := fmt.Sprintf("Claim: %s\nToday: %s\n\n%s", claim, now.Format("2006-01-02"), formatResults(results))
	if err := c.LLM.JSON(ctx, TaskAssess, AssessSystemPrompt, user, &out, run); err != nil {
		return Assessment{Verdict: core.VerdictUnverifiable, Confidence: core.ConfidenceNone, Inference: "The analyst model could not be reached: " + err.Error()}
	}
	a := Assessment{Verdict: strings.ToLower(string(out.Verdict)), CurrentValue: string(out.CurrentValue),
		Confidence: core.ConfidenceNone, Inference: string(out.Reasoning)}
	if a.Verdict != core.VerdictSupported && a.Verdict != core.VerdictChanged {
		a.Verdict, a.CurrentValue = core.VerdictUnverifiable, ""
		return a
	}
	var src *Result
	for i := range results {
		if results[i].ID == strings.TrimSpace(string(out.Source)) {
			src = &results[i]
		}
	}
	quote := strings.TrimSpace(string(out.Quote))
	if src == nil || !evidence.Grounded(quote, src.Text) {
		a.Verdict, a.CurrentValue = core.VerdictUnverifiable, ""
		a.Inference = strings.TrimSpace(a.Inference + " Heimdall set this to unverifiable because the analyst's supporting quote was not found in the retrieved source.")
		return a
	}
	a.Evidence = append(a.Evidence, core.Evidence{
		ID: src.ID, Kind: "web", Via: "tavily.search", URL: src.URL, Domain: evidence.DomainOf(src.URL), Title: src.Title,
		Quote: quote, Grounded: true, PublishedDate: src.Published, RetrievedAt: now,
	})
	// Confidence is high only when a second, independent domain states the same value.
	a.Confidence = core.ConfidenceMedium
	if cv := evidence.Canon(a.CurrentValue); len(cv) >= 2 {
		for _, r := range results {
			if r.ID == src.ID || evidence.DomainOf(r.URL) == evidence.DomainOf(src.URL) {
				continue
			}
			if containsToken(evidence.Canon(r.Text), cv) {
				a.Confidence = core.ConfidenceHigh
				a.Evidence = append(a.Evidence, core.Evidence{
					ID: r.ID, Kind: "web", Via: "tavily.search", URL: r.URL, Domain: evidence.DomainOf(r.URL), Title: r.Title,
					Quote: snippetAround(r.Text, a.CurrentValue), Grounded: true, PublishedDate: r.Published, RetrievedAt: now,
				})
				break
			}
		}
	}
	return a
}

// containsToken reports whether needle occurs in haystack on word boundaries
// (both already canonical).
func containsToken(haystack, needle string) bool {
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

// snippetAround returns the sentence-sized span of text around value.
func snippetAround(text, value string) string {
	i := strings.Index(strings.ToLower(text), strings.ToLower(value))
	if i < 0 {
		// value matched canonically but not literally; return the opening of the text
		if len(text) > 200 {
			return strings.TrimSpace(text[:200])
		}
		return strings.TrimSpace(text)
	}
	lo, hi := i, i+len(value)
	for lo > 0 && i-lo < 120 && !strings.ContainsRune(".!?\n", rune(text[lo-1])) {
		lo--
	}
	for hi < len(text) && hi-i < 160 && !strings.ContainsRune(".!?\n", rune(text[hi])) {
		hi++
	}
	if hi < len(text) {
		hi++
	}
	return strings.TrimSpace(text[lo:hi])
}

// Check runs the whole pipeline on one answer.
func (c *Checker) Check(ctx context.Context, req Request) core.FreshnessReport {
	start := c.Now()
	s0, _ := c.Web.Counts()
	l0 := c.LLM.Calls()
	rep := core.FreshnessReport{At: start.UTC(), Question: req.Question, Answer: req.Answer, Model: req.Model,
		Source: req.Source, Claims: []core.Claim{}, Score: -1, AnalystUsed: c.LLM.ModelName()}
	finish := func() core.FreshnessReport {
		s1, _ := c.Web.Counts()
		rep.Searches = int(s1 - s0)
		rep.LLMCalls = int(c.LLM.Calls() - l0)
		rep.ElapsedMs = c.Now().Sub(start).Milliseconds()
		return rep
	}
	if strings.TrimSpace(req.Answer) == "" {
		rep.Error = "no answer text to check"
		return finish()
	}

	// 1. Claims.
	var ex struct {
		Claims []claimOut `json:"claims"`
	}
	user := fmt.Sprintf("Today: %s\n\nQuestion:\n%s\n\nAnswer:\n%s", start.UTC().Format("2006-01-02"), orDash(req.Question), req.Answer)
	if err := c.LLM.JSON(ctx, TaskClaims, ClaimsSystemPrompt, user, &ex, req.Run); err != nil {
		rep.Error = err.Error()
		return finish()
	}
	for i, co := range ex.Claims {
		text := strings.TrimSpace(string(co.Text))
		if text == "" {
			continue
		}
		rep.Claims = append(rep.Claims, core.Claim{
			ID: fmt.Sprintf("c%d", i+1), Text: text, TimeSensitive: bool(co.TimeSensitive),
			Category: orOther(string(co.Category)), Query: strings.TrimSpace(string(co.Query)), Evidence: []core.Evidence{},
		})
	}

	// 2. Re-check the time-sensitive ones against the live web.
	checked := 0
	for i := range rep.Claims {
		cl := &rep.Claims[i]
		if !cl.TimeSensitive || checked >= c.MaxClaims {
			if cl.TimeSensitive {
				cl.Inference = fmt.Sprintf("Not checked: this run checks at most %d time-sensitive claims.", c.MaxClaims)
			}
			continue
		}
		checked++
		cl.Checked = true
		query := cl.Query
		if query == "" {
			query = cl.Text
		}
		results, err := c.Search(ctx, query)
		if err != nil {
			cl.Verdict, cl.Confidence = core.VerdictUnverifiable, core.ConfidenceNone
			cl.Inference = "Search failed: " + err.Error()
			if rep.Error == "" {
				rep.Error = err.Error()
			}
			continue
		}
		a := c.Assess(ctx, cl.Text, results, req.Run)
		cl.Verdict, cl.CurrentValue, cl.Confidence, cl.Inference = a.Verdict, a.CurrentValue, a.Confidence, a.Inference
		if a.Evidence != nil {
			cl.Evidence = a.Evidence
		}
	}

	for _, cl := range rep.Claims {
		switch {
		case !cl.Checked:
			rep.NotChecked++
		case cl.Verdict == core.VerdictSupported:
			rep.Supported++
		case cl.Verdict == core.VerdictChanged:
			rep.Changed++
		default:
			rep.Unverified++
		}
	}
	if d := rep.Supported + rep.Changed; d > 0 {
		rep.Score = float64(rep.Supported) / float64(d)
	}

	// 3. Optionally rewrite the answer with what changed.
	if req.Rewrite && rep.Changed > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Today: %s\n\nQuestion:\n%s\n\nOriginal answer:\n%s\n\nFindings:\n", start.UTC().Format("2006-01-02"), orDash(req.Question), req.Answer)
		n := 0
		for _, cl := range rep.Claims {
			if !cl.Checked {
				continue
			}
			url := ""
			if len(cl.Evidence) > 0 {
				url = cl.Evidence[0].URL
			}
			switch cl.Verdict {
			case core.VerdictChanged:
				n++
				fmt.Fprintf(&b, "- [%d] CHANGED: %q is no longer accurate. Current fact: %s. Source: %s\n", n, cl.Text, cl.CurrentValue, url)
			case core.VerdictUnverifiable:
				fmt.Fprintf(&b, "- UNVERIFIABLE: %q\n", cl.Text)
			}
		}
		if text, err := c.LLM.Text(ctx, TaskRewrite, RewriteSystemPrompt, b.String(), req.Run); err == nil {
			rep.Rewrite = strings.TrimSpace(text)
		} else {
			c.Log.Warn("rewrite failed", "err", err)
		}
	}
	return finish()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not provided)"
	}
	return s
}

func orOther(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "version", "price", "role", "status", "statistic", "policy", "schedule":
		return s
	}
	return "other"
}
