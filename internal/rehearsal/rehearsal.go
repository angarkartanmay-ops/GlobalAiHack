// Package rehearsal answers the question a retirement notice raises: is the
// replacement safe for the traffic this gateway actually serves?
//
// It samples real requests from the gateway's logs, replays each against the
// candidate model through the gateway, and compares the two answers. Nothing
// is asked of the application and no production request is touched: replays
// run after the fact, under Heimdall's own budgeted virtual key.
package rehearsal

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/analyst"
	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/freshness"
)

// TaskJudge is the analyst task name for comparing two answers.
const TaskJudge = "replay_judge"

// JudgeSystemPrompt instructs the judge. It is told not to pick a winner on
// facts that can change over time: both answers come from training data, so
// that question goes to live evidence instead.
const JudgeSystemPrompt = `You compare two answers to the same request. BASELINE comes from the model serving production today. CANDIDATE comes from the model that would replace it. Decide whether switching to the candidate would change what the application or its user receives.

Output one object:
{"verdict": "equivalent" | "better" | "acceptable" | "regression",
 "reason": "one sentence",
 "disagreements": [{"topic": "a web search query that would settle it", "baseline": "the baseline's claim as a standalone sentence", "candidate": "the candidate's claim as a standalone sentence"}]}

- "equivalent": same facts, same instructions followed, same format. Wording may differ.
- "better": the candidate is more complete or follows the request's instructions more closely.
- "acceptable": differences of style, length or detail that would not matter to the application.
- "regression": the candidate is wrong, leaves out something the request required, ignores an instruction, or changes a format the application depends on.

"disagreements" lists ONLY places where the two answers state different facts about something that can change over time: a version, a price, who holds a role, whether something is available, a statistic. Do not decide which is right. You cannot know: both answers come from training data that may be out of date, and so does your own knowledge. When such a disagreement is the only difference, use the verdict "acceptable"; it will be settled separately with live evidence. Use [] when there are none.`

type judgeOut struct {
	Verdict       analyst.Str `json:"verdict"`
	Reason        analyst.Str `json:"reason"`
	Disagreements []struct {
		Topic     analyst.Str `json:"topic"`
		Baseline  analyst.Str `json:"baseline"`
		Candidate analyst.Str `json:"candidate"`
	} `json:"disagreements"`
}

// Gateway is the part of the Bifrost client a rehearsal uses.
type Gateway interface {
	Logs(ctx context.Context, f bifrost.LogFilter) (*bifrost.LogPage, error)
	LogDetail(ctx context.Context, id string) (*bifrost.Log, error)
	Chat(ctx context.Context, req bifrost.ChatRequest, tags bifrost.Tags) (*bifrost.ChatResponse, error)
	ListModels(ctx context.Context) ([]bifrost.ModelInfo, error)
}

// Runner runs rehearsals.
type Runner struct {
	GW          Gateway
	Judge       analyst.LLM
	Fresh       *freshness.Checker // optional; settles time-sensitive disagreements
	Now         func() time.Time
	Log         *slog.Logger
	Lookback    time.Duration
	Concurrency int
}

// Options configures one rehearsal.
type Options struct {
	ID           string
	Baseline     string  // provider/model in production
	Candidate    string  // provider/model to rehearse
	Samples      int     // real requests to replay
	BudgetUSD    float64 // stop replaying once this much has been spent; 0 = no limit beyond the gateway's
	MaxTiebreaks int     // time-sensitive disagreements settled with live evidence
}

// sample is a logged request ready to replay.
type sample struct {
	log       *bifrost.Log
	wantsJSON bool
}

// collect picks real requests to replay: recent, successful, not Heimdall's
// own, spread across consumers, and distinct.
func (r *Runner) collect(ctx context.Context, base core.ModelRef, n int) ([]sample, error) {
	now := r.Now()
	filter := bifrost.LogFilter{Providers: []string{base.Provider}, Models: []string{base.Model}, Status: []string{"success"},
		Start: now.Add(-r.Lookback), End: now, Limit: 100}
	byConsumer := map[string][]bifrost.Log{}
	var consumers []string
	for page := 0; page < 5; page++ {
		filter.Offset = page * 100
		p, err := r.GW.Logs(ctx, filter)
		if err != nil {
			return nil, err
		}
		for _, l := range p.Logs {
			if l.Object != "chat_completion" || l.IsHeimdall() || l.ContentHidden || l.Stream && l.OutputMessage == nil {
				continue
			}
			c := l.Consumer()
			if _, ok := byConsumer[c]; !ok {
				consumers = append(consumers, c)
			}
			byConsumer[c] = append(byConsumer[c], l)
		}
		if len(p.Logs) < 100 {
			break
		}
	}
	sort.Strings(consumers)

	// Round-robin across consumers so one busy application does not crowd
	// out the others, then fetch details until n distinct requests are found.
	var order []bifrost.Log
	for i := 0; ; i++ {
		added := false
		for _, c := range consumers {
			if i < len(byConsumer[c]) {
				order = append(order, byConsumer[c][i])
				added = true
			}
		}
		if !added {
			break
		}
	}
	seen := map[string]bool{}
	var out []sample
	for _, l := range order {
		if len(out) >= n {
			break
		}
		d, err := r.GW.LogDetail(ctx, l.ID)
		if err != nil || d == nil || len(d.InputHistory) == 0 || string(d.InputHistory) == "null" || d.OutputMessage == nil {
			continue
		}
		h := sha1.Sum(append(append([]byte{}, d.InputHistory...), d.Params["tools"]...))
		key := hex.EncodeToString(h[:])
		if seen[key] {
			continue
		}
		seen[key] = true
		s := sample{log: d}
		if rf, ok := d.Params["response_format"]; ok && strings.Contains(string(rf), "json") {
			s.wantsJSON = true
		}
		out = append(out, s)
	}
	return out, nil
}

func outputFromMessage(m *bifrost.Message, finish string) core.Output {
	var o core.Output
	if m == nil {
		return o
	}
	o.Content = m.Text()
	o.FinishReason = finish
	for _, tc := range m.ToolCalls {
		o.ToolCalls = append(o.ToolCalls, core.ToolCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return o
}

// lastUserText returns the last user message of a conversation, for display.
func lastUserText(history json.RawMessage) (string, int) {
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(history, &msgs) != nil {
		return "", 0
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return bifrost.ContentText(msgs[i].Content), len(msgs)
		}
	}
	return "", len(msgs)
}

// replayParams are the logged parameters that are safe and meaningful to
// send again. Anything else in the log's params is dropped.
var replayParams = map[string]bool{
	"temperature": true, "top_p": true, "max_tokens": true, "max_completion_tokens": true, "stop": true,
	"presence_penalty": true, "frequency_penalty": true, "seed": true, "response_format": true,
	"tools": true, "tool_choice": true, "parallel_tool_calls": true, "reasoning_effort": true, "n": true,
}

func (r *Runner) replay(ctx context.Context, s sample, candidate string, price pricing, run string) core.Output {
	extra := map[string]json.RawMessage{}
	for k, v := range s.log.Params {
		if replayParams[k] && len(v) > 0 && string(v) != "null" {
			extra[k] = v
		}
	}
	resp, err := r.GW.Chat(ctx, bifrost.ChatRequest{Model: candidate, Messages: s.log.InputHistory, Extra: extra},
		bifrost.Tags{Kind: "replay", Run: run})
	if err != nil {
		return core.Output{Error: err.Error()}
	}
	msg, finish := resp.First()
	o := outputFromMessage(&msg, finish)
	o.LatencyMs = float64(resp.Elapsed.Microseconds()) / 1000
	o.PromptTokens, o.OutputTokens = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	o.Cost = float64(o.PromptTokens)*price.prompt + float64(o.OutputTokens)*price.completion
	return o
}

type pricing struct{ prompt, completion float64 }

// Run rehearses a candidate against real traffic. progress, when not nil, is
// called with partial state so a UI can follow along.
func (r *Runner) Run(ctx context.Context, opt Options, progress func(core.Rehearsal)) core.Rehearsal {
	if opt.Samples <= 0 {
		opt.Samples = 25
	}
	if opt.MaxTiebreaks <= 0 {
		opt.MaxTiebreaks = 6
	}
	conc := r.Concurrency
	if conc <= 0 {
		conc = 4
	}
	reh := core.Rehearsal{ID: opt.ID, Baseline: opt.Baseline, Candidate: opt.Candidate, Status: "running",
		StartedAt: r.Now().UTC(), Requested: opt.Samples, BudgetUSD: opt.BudgetUSD,
		Outcomes: map[string]int{}, CheckFailures: map[string]int{}, Samples: []core.Sample{}}
	if r.Judge != nil {
		reh.JudgeModel = r.Judge.ModelName()
	}
	report := func() {
		if progress != nil {
			progress(reh)
		}
	}
	fail := func(err error) core.Rehearsal {
		reh.Status, reh.Error, reh.FinishedAt = "failed", err.Error(), r.Now().UTC()
		report()
		return reh
	}
	report()

	if opt.Baseline == opt.Candidate {
		return fail(errors.New("baseline and candidate are the same model"))
	}
	samples, err := r.collect(ctx, core.ParseModelRef(opt.Baseline), opt.Samples)
	if err != nil {
		return fail(fmt.Errorf("reading gateway logs: %w", err))
	}
	if len(samples) == 0 {
		return fail(fmt.Errorf("no replayable chat requests for %s in the last %s of gateway logs (content logging must be on)", opt.Baseline, r.Lookback))
	}
	reh.Sampled = len(samples)

	var price pricing
	if models, err := r.GW.ListModels(ctx); err == nil {
		for _, m := range models {
			if m.ID == opt.Candidate {
				price = pricing{m.PromptPrice(), m.CompletionPrice()}
			}
		}
	}

	// Replay with bounded concurrency, stopping when the budget is spent.
	results := make([]core.Sample, len(samples))
	done := make([]bool, len(samples))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	stop := ""
	for i := range samples {
		mu.Lock()
		halt := stop != ""
		mu.Unlock()
		if halt || ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			s := samples[i]
			prompt, n := lastUserText(s.log.InputHistory)
			sm := core.Sample{LogID: s.log.ID, At: s.log.Timestamp, Consumer: s.log.Consumer(), Prompt: prompt, Messages: n,
				HasTools: len(s.log.Params["tools"]) > 0 && string(s.log.Params["tools"]) != "null"}
			sm.Baseline = outputFromMessage(s.log.OutputMessage, s.log.StopReason)
			sm.Baseline.LatencyMs, sm.Baseline.Cost = s.log.Latency, s.log.Cost
			if s.log.TokenUsage != nil {
				sm.Baseline.PromptTokens, sm.Baseline.OutputTokens = s.log.TokenUsage.PromptTokens, s.log.TokenUsage.CompletionTokens
			}
			sm.Candidate = r.replay(ctx, s, opt.Candidate, price, opt.ID)
			sm.Checks = Compare(sm.Baseline, sm.Candidate, s.wantsJSON)

			mu.Lock()
			results[i], done[i] = sm, true
			reh.SpendUSD += sm.Candidate.Cost
			reh.Completed++
			if sm.Candidate.Error != "" && isBudgetError(sm.Candidate.Error) && stop == "" {
				stop = "The gateway refused further replays: the budget on Heimdall's virtual key is exhausted."
			}
			if opt.BudgetUSD > 0 && reh.SpendUSD >= opt.BudgetUSD && stop == "" {
				stop = fmt.Sprintf("Stopped at the rehearsal budget of $%.2f.", opt.BudgetUSD)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	reh.StoppedEarly = stop

	// Judge what the deterministic checks could not settle, in order.
	tiebreaks := 0
	for i := range results {
		if !done[i] {
			continue
		}
		sm := &results[i]
		r.settle(ctx, sm, &tiebreaks, opt)
		reh.Samples = append(reh.Samples, *sm)
		summarize(&reh)
		report()
	}

	reh.Status, reh.FinishedAt = "done", r.Now().UTC()
	summarize(&reh)
	report()
	return reh
}

func isBudgetError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "budget") || strings.HasPrefix(m, "bifrost 402")
}

// settle assigns an outcome to one sample.
func (r *Runner) settle(ctx context.Context, sm *core.Sample, tiebreaks *int, opt Options) {
	if sm.Candidate.Error != "" {
		sm.Outcome, sm.JudgeReason = core.OutcomeError, "The candidate did not return an answer."
		return
	}
	if c, failed := HardFailure(sm.Checks); failed {
		sm.Outcome, sm.JudgeReason = core.OutcomeRegression, c.Detail
		return
	}
	if TriviallyEquivalent(sm.Baseline, sm.Candidate) {
		sm.Outcome, sm.JudgeReason = core.OutcomeEquivalent, "Identical output."
		return
	}
	if r.Judge == nil {
		sm.Outcome, sm.JudgeReason = core.OutcomeAcceptable, "Outputs differ; no judge model is configured, so only the deterministic checks were applied."
		return
	}
	var out judgeOut
	user := fmt.Sprintf("REQUEST (last user message):\n%s\n\nBASELINE:\n%s\n\nCANDIDATE:\n%s",
		clip(sm.Prompt, 3000), clip(renderOutput(sm.Baseline), 4000), clip(renderOutput(sm.Candidate), 4000))
	if err := r.Judge.JSON(ctx, TaskJudge, JudgeSystemPrompt, user, &out, opt.ID); err != nil {
		sm.Outcome, sm.JudgeReason = core.OutcomeAcceptable, "The judge model could not be reached ("+err.Error()+"); only the deterministic checks were applied."
		return
	}
	switch v := strings.ToLower(string(out.Verdict)); v {
	case core.OutcomeEquivalent, core.OutcomeBetter, core.OutcomeAcceptable, core.OutcomeRegression:
		sm.Outcome = v
	default:
		sm.Outcome = core.OutcomeAcceptable
	}
	sm.JudgeReason = string(out.Reason)

	// Where the two models disagree about something that changes over time,
	// neither is assumed right. Live evidence decides.
	if r.Fresh == nil {
		return
	}
	for _, d := range out.Disagreements {
		if *tiebreaks >= opt.MaxTiebreaks {
			break
		}
		topic, bc, cc := string(d.Topic), string(d.Baseline), string(d.Candidate)
		if topic == "" || bc == "" || cc == "" {
			continue
		}
		*tiebreaks++
		tb := core.FreshnessTiebreak{Topic: topic, BaselineClaim: bc, CandidateClaim: cc, Winner: "neither", Evidence: []core.Evidence{}}
		results, err := r.Fresh.Search(ctx, topic)
		if err != nil {
			tb.BaselineVerdict, tb.CandidateVerdict = core.VerdictUnverifiable, core.VerdictUnverifiable
			sm.Tiebreaks = append(sm.Tiebreaks, tb)
			continue
		}
		ba := r.Fresh.Assess(ctx, bc, results, opt.ID)
		ca := r.Fresh.Assess(ctx, cc, results, opt.ID)
		tb.BaselineVerdict, tb.CandidateVerdict = ba.Verdict, ca.Verdict
		tb.Evidence = append(tb.Evidence, ca.Evidence...)
		if len(tb.Evidence) == 0 {
			tb.Evidence = append(tb.Evidence, ba.Evidence...)
		}
		switch {
		case ca.Verdict == core.VerdictSupported && ba.Verdict == core.VerdictChanged:
			tb.Winner = "candidate"
		case ba.Verdict == core.VerdictSupported && ca.Verdict == core.VerdictChanged:
			tb.Winner = "baseline"
		}
		sm.Tiebreaks = append(sm.Tiebreaks, tb)
	}
	fresher, staler := 0, 0
	for _, tb := range sm.Tiebreaks {
		switch tb.Winner {
		case "candidate":
			fresher++
		case "baseline":
			staler++
		}
	}
	switch {
	case staler > 0:
		sm.Outcome = core.OutcomeRegression
		sm.JudgeReason = "Live evidence shows the candidate's answer is out of date where the baseline's is current. " + sm.JudgeReason
	case fresher > 0 && sm.Outcome != core.OutcomeRegression:
		sm.Outcome = core.OutcomeBetter
		sm.JudgeReason = "Live evidence shows the candidate's answer is current where the baseline's is out of date. " + sm.JudgeReason
	}
}

func renderOutput(o core.Output) string {
	var b strings.Builder
	b.WriteString(o.Content)
	for _, tc := range o.ToolCalls {
		fmt.Fprintf(&b, "\n[tool call] %s(%s)", tc.Name, tc.Arguments)
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n…[truncated]"
	}
	return s
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64{}, v...)
	sort.Float64s(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	} else {
		return (s[n/2-1] + s[n/2]) / 2
	}
}

// summarize recomputes the aggregate fields from the samples settled so far.
func summarize(reh *core.Rehearsal) {
	reh.Outcomes, reh.CheckFailures = map[string]int{}, map[string]int{}
	reh.BaselineCost, reh.CandidateCost, reh.FresherCount, reh.StalerCount = 0, 0, 0, 0
	var bl, cl []float64
	pass := 0
	for _, s := range reh.Samples {
		reh.Outcomes[s.Outcome]++
		for _, c := range s.Checks {
			if !c.Pass {
				reh.CheckFailures[c.Name]++
			}
		}
		for _, tb := range s.Tiebreaks {
			switch tb.Winner {
			case "candidate":
				reh.FresherCount++
			case "baseline":
				reh.StalerCount++
			}
		}
		if s.Outcome != core.OutcomeRegression && s.Outcome != core.OutcomeError {
			pass++
		}
		if s.Candidate.Error == "" {
			reh.BaselineCost += s.Baseline.Cost
			reh.CandidateCost += s.Candidate.Cost
			if s.Baseline.LatencyMs > 0 {
				bl = append(bl, s.Baseline.LatencyMs)
			}
			if s.Candidate.LatencyMs > 0 {
				cl = append(cl, s.Candidate.LatencyMs)
			}
		}
	}
	n := len(reh.Samples)
	if n == 0 {
		return
	}
	reh.Score = 100 * float64(pass) / float64(n)
	reh.BaselineP50, reh.CandidateP50 = median(bl), median(cl)
	reh.CostDeltaPct, reh.LatencyDelta = 0, 0
	if reh.BaselineCost > 0 && reh.CandidateCost > 0 {
		reh.CostDeltaPct = 100 * (reh.CandidateCost - reh.BaselineCost) / reh.BaselineCost
	}
	if reh.BaselineP50 > 0 && reh.CandidateP50 > 0 {
		reh.LatencyDelta = 100 * (reh.CandidateP50 - reh.BaselineP50) / reh.BaselineP50
	}

	fails := reh.Outcomes[core.OutcomeRegression] + reh.Outcomes[core.OutcomeError]
	switch {
	case fails == 0:
		reh.Verdict = core.ReadyYes
		reh.Summary = fmt.Sprintf("All %d replayed requests came back equivalent or better.", n)
	case reh.Score >= 90:
		reh.Verdict = core.ReadyCaveats
		reh.Summary = fmt.Sprintf("%d of %d replayed requests regressed. Review them before cutting over.", fails, n)
	default:
		reh.Verdict = core.ReadyNo
		reh.Summary = fmt.Sprintf("%d of %d replayed requests regressed. This replacement is not a drop-in for this traffic.", fails, n)
	}
	if n < 10 {
		reh.Summary += fmt.Sprintf(" Only %d distinct requests were available to replay, so treat this as indicative.", n)
	}
	if reh.FresherCount > 0 || reh.StalerCount > 0 {
		reh.Summary += fmt.Sprintf(" On time-sensitive facts checked against the live web, the candidate was more current %d time(s) and less current %d time(s).", reh.FresherCount, reh.StalerCount)
	}
}
