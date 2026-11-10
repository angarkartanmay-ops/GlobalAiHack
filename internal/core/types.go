// Package core holds the domain types shared by every Heimdall component.
package core

import (
	"strings"
	"time"
)

// ModelRef identifies a model the way Bifrost does: provider plus model name.
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// ID returns the "provider/model" form used in Bifrost requests.
func (m ModelRef) ID() string {
	if m.Provider == "" {
		return m.Model
	}
	return m.Provider + "/" + m.Model
}

// ParseModelRef splits "provider/model". Only the first slash separates the
// provider, because model names may themselves contain slashes.
func ParseModelRef(s string) ModelRef {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "/"); i > 0 {
		return ModelRef{Provider: s[:i], Model: s[i+1:]}
	}
	return ModelRef{Model: s}
}

// ---------------------------------------------------------------------------
// Inventory: what the gateway is actually serving.
// ---------------------------------------------------------------------------

// ConsumerUsage is one virtual key's share of a model's traffic.
type ConsumerUsage struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
}

// InventoryItem is one model observed in gateway traffic over the lookback
// window. Every number comes from the gateway's own log aggregates.
type InventoryItem struct {
	ModelRef
	Requests     int             `json:"requests"`
	SuccessRate  float64         `json:"success_rate"` // 0..100
	Tokens       int             `json:"tokens"`
	Cost         float64         `json:"cost"`
	AvgLatencyMs float64         `json:"avg_latency_ms"`
	RequestShare float64         `json:"request_share"` // 0..1 of all observed requests
	CostShare    float64         `json:"cost_share"`    // 0..1 of all observed cost
	DailyReqs    float64         `json:"daily_requests"`
	DailyCost    float64         `json:"daily_cost"`
	Consumers    []ConsumerUsage `json:"consumers"`
}

// Inventory is a snapshot of traffic by model.
type Inventory struct {
	TakenAt     time.Time       `json:"taken_at"`
	WindowStart time.Time       `json:"window_start"`
	WindowEnd   time.Time       `json:"window_end"`
	WindowDays  float64         `json:"window_days"`
	Requests    int             `json:"requests"`
	Cost        float64         `json:"cost"`
	Items       []InventoryItem `json:"items"`
}

// ---------------------------------------------------------------------------
// Evidence and findings: what the outside world says about those models.
// ---------------------------------------------------------------------------

// Lifecycle status of a model.
const (
	StatusActive     = "active"     // a source positively says it is current
	StatusDeprecated = "deprecated" // announced for retirement, still serving
	StatusRetired    = "retired"    // shutdown date has passed, or source says removed
	StatusNoNotice   = "no_notice"  // searched, found nothing that names this model
	StatusUnknown    = "unknown"    // not checked yet, or the check failed
)

// Confidence levels for a finding.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
	ConfidenceNone   = "none"
)

// Evidence is one piece of retrieved, attributable source material. Heimdall
// keeps a piece of web evidence only when its quote was found verbatim in the
// text Tavily returned for that URL (Grounded == true).
type Evidence struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"` // "web" or "gateway"
	Via           string    `json:"via"`  // tavily.search, tavily.extract, bifrost.models
	URL           string    `json:"url,omitempty"`
	Domain        string    `json:"domain,omitempty"`
	Title         string    `json:"title,omitempty"`
	Quote         string    `json:"quote"`
	Grounded      bool      `json:"grounded"`
	Official      bool      `json:"official"` // domain is on the provider's own documentation list
	Applies       string    `json:"applies"`  // exact, alias, family
	Status        string    `json:"status,omitempty"`
	ShutdownDate  string    `json:"shutdown_date,omitempty"` // YYYY-MM-DD, parsed from the quote itself
	DatePrecision string    `json:"date_precision,omitempty"`
	Replacement   string    `json:"replacement,omitempty"`
	PublishedDate string    `json:"published_date,omitempty"`
	RetrievedAt   time.Time `json:"retrieved_at"`
}

// Conflict records sources that disagree on the shutdown date.
type Conflict struct {
	Dates   []string `json:"dates"`
	Summary string   `json:"summary"`
}

// Change records that a finding moved between two scans.
type Change struct {
	At    time.Time `json:"at"`
	Field string    `json:"field"`
	From  string    `json:"from"`
	To    string    `json:"to"`
}

// Finding is Heimdall's conclusion about one model. Evidence is what sources
// say; Inference is what Heimdall or the analyst model concluded beyond that.
// The two are never mixed.
type Finding struct {
	ModelRef
	Status           string     `json:"status"`
	ShutdownDate     string     `json:"shutdown_date,omitempty"`
	DatePrecision    string     `json:"date_precision,omitempty"`
	Replacement      string     `json:"replacement,omitempty"`
	ReplacementBasis string     `json:"replacement_basis,omitempty"` // stated or inferred
	ReplacementOnGW  bool       `json:"replacement_on_gateway"`
	Confidence       string     `json:"confidence"`
	Evidence         []Evidence `json:"evidence"`
	Inference        []string   `json:"inference"`
	Discarded        int        `json:"discarded_ungrounded"` // analyst quotes that failed grounding
	Conflict         *Conflict  `json:"conflict,omitempty"`
	FirstSeen        time.Time  `json:"first_seen"`
	LastVerified     time.Time  `json:"last_verified"`
	Changes          []Change   `json:"changes,omitempty"`
	Error            string     `json:"error,omitempty"`
}

// Risk levels for an exposure.
const (
	RiskCritical = "critical"
	RiskHigh     = "high"
	RiskMedium   = "medium"
	RiskLow      = "low"
	RiskNone     = "none"
)

// Exposure joins a finding with live traffic: the thing an operator acts on.
type Exposure struct {
	ModelRef
	Finding   *Finding       `json:"finding,omitempty"`
	Traffic   *InventoryItem `json:"traffic,omitempty"`
	DaysLeft  *int           `json:"days_left,omitempty"`
	Risk      string         `json:"risk"`
	Migration *Migration     `json:"migration,omitempty"`
	Rehearsal *RehearsalRef  `json:"rehearsal,omitempty"`
}

// RehearsalRef is the latest rehearsal summary shown next to an exposure.
type RehearsalRef struct {
	ID        string    `json:"id"`
	Candidate string    `json:"candidate"`
	Verdict   string    `json:"verdict"`
	Score     float64   `json:"score"`
	At        time.Time `json:"at"`
}

// Scan is one run of the radar across the inventory.
type Scan struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"` // running, done, failed
	Models     int       `json:"models"`
	AtRisk     int       `json:"at_risk"`
	Searches   int       `json:"tavily_searches"`
	Extracts   int       `json:"tavily_extracts"`
	LLMCalls   int       `json:"llm_calls"`
	Error      string    `json:"error,omitempty"`
	Trigger    string    `json:"trigger"` // schedule, manual, startup
}

// ---------------------------------------------------------------------------
// Freshness: the stale-answer detector.
// ---------------------------------------------------------------------------

// Claim verdicts.
const (
	VerdictSupported    = "supported"
	VerdictChanged      = "changed"
	VerdictUnverifiable = "unverifiable"
)

// Claim is one atomic statement pulled out of an LLM answer.
type Claim struct {
	ID            string     `json:"id"`
	Text          string     `json:"text"`
	TimeSensitive bool       `json:"time_sensitive"`
	Category      string     `json:"category"`
	Query         string     `json:"query,omitempty"`
	Checked       bool       `json:"checked"`
	Verdict       string     `json:"verdict,omitempty"`
	CurrentValue  string     `json:"current_value,omitempty"`
	Confidence    string     `json:"confidence,omitempty"`
	Evidence      []Evidence `json:"evidence"`
	Inference     string     `json:"inference,omitempty"` // analyst reasoning, kept apart from evidence
}

// FreshnessReport is the result of checking one answer.
type FreshnessReport struct {
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
	Question    string    `json:"question,omitempty"`
	Answer      string    `json:"answer"`
	Model       string    `json:"model,omitempty"`
	Claims      []Claim   `json:"claims"`
	Supported   int       `json:"supported"`
	Changed     int       `json:"changed"`
	Unverified  int       `json:"unverifiable"`
	NotChecked  int       `json:"not_checked"`
	Score       float64   `json:"freshness_score"` // supported / checked, 0..1; -1 when nothing was checked
	Rewrite     string    `json:"rewrite,omitempty"`
	Searches    int       `json:"tavily_searches"`
	LLMCalls    int       `json:"llm_calls"`
	Source      string    `json:"source"` // manual, rehearsal, mcp
	Error       string    `json:"error,omitempty"`
	ElapsedMs   int64     `json:"elapsed_ms"`
	AnalystUsed string    `json:"analyst_model,omitempty"`
}

// ---------------------------------------------------------------------------
// Rehearsal: replaying real traffic against a replacement.
// ---------------------------------------------------------------------------

// Sample outcomes.
const (
	OutcomeEquivalent = "equivalent"
	OutcomeBetter     = "better"
	OutcomeAcceptable = "acceptable"
	OutcomeRegression = "regression"
	OutcomeError      = "error"
)

// Check is one deterministic comparison between baseline and candidate.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// ToolCall is a normalised tool invocation.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Output is a normalised chat completion result.
type Output struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	LatencyMs    float64    `json:"latency_ms"`
	PromptTokens int        `json:"prompt_tokens"`
	OutputTokens int        `json:"output_tokens"`
	Cost         float64    `json:"cost"`
	Error        string     `json:"error,omitempty"`
}

// FreshnessTiebreak records a time-sensitive disagreement settled by live evidence.
type FreshnessTiebreak struct {
	Topic            string     `json:"topic"`
	BaselineClaim    string     `json:"baseline_claim"`
	CandidateClaim   string     `json:"candidate_claim"`
	BaselineVerdict  string     `json:"baseline_verdict"`
	CandidateVerdict string     `json:"candidate_verdict"`
	Winner           string     `json:"winner"` // baseline, candidate, neither
	Evidence         []Evidence `json:"evidence"`
}

// Sample is one real request replayed against the candidate.
type Sample struct {
	LogID       string              `json:"log_id"`
	At          time.Time           `json:"at"`
	Consumer    string              `json:"consumer,omitempty"`
	Prompt      string              `json:"prompt"` // last user message, for display
	Messages    int                 `json:"messages"`
	HasTools    bool                `json:"has_tools"`
	Baseline    Output              `json:"baseline"`
	Candidate   Output              `json:"candidate"`
	Checks      []Check             `json:"checks"`
	Outcome     string              `json:"outcome"`
	JudgeReason string              `json:"judge_reason,omitempty"`
	Tiebreaks   []FreshnessTiebreak `json:"tiebreaks,omitempty"`
}

// Rehearsal verdicts.
const (
	ReadyYes     = "ready"
	ReadyCaveats = "ready_with_caveats"
	ReadyNo      = "not_ready"
)

// Rehearsal is a full shadow run of real traffic against one candidate.
type Rehearsal struct {
	ID            string         `json:"id"`
	Baseline      string         `json:"baseline"`
	Candidate     string         `json:"candidate"`
	Status        string         `json:"status"` // running, done, failed
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	Requested     int            `json:"requested"`
	Sampled       int            `json:"sampled"`
	Completed     int            `json:"completed"`
	Outcomes      map[string]int `json:"outcomes"`
	CheckFailures map[string]int `json:"check_failures"`
	Score         float64        `json:"score"` // 0..100
	Verdict       string         `json:"verdict"`
	Summary       string         `json:"summary"`
	BaselineCost  float64        `json:"baseline_cost"`
	CandidateCost float64        `json:"candidate_cost"`
	CostDeltaPct  float64        `json:"cost_delta_pct"`
	BaselineP50   float64        `json:"baseline_latency_p50_ms"`
	CandidateP50  float64        `json:"candidate_latency_p50_ms"`
	LatencyDelta  float64        `json:"latency_delta_pct"`
	FresherCount  int            `json:"candidate_fresher"`
	StalerCount   int            `json:"candidate_staler"`
	SpendUSD      float64        `json:"spend_usd"` // what the rehearsal itself cost
	BudgetUSD     float64        `json:"budget_usd"`
	StoppedEarly  string         `json:"stopped_early,omitempty"`
	Samples       []Sample       `json:"samples"`
	Error         string         `json:"error,omitempty"`
	JudgeModel    string         `json:"judge_model,omitempty"`
}

// ---------------------------------------------------------------------------
// Migration: acting on the gateway through routing rules.
// ---------------------------------------------------------------------------

// Migration stages.
const (
	StageNone     = "none"
	StageArmed    = "armed"    // safety net: old model primary, replacement as fallback
	StageCanary   = "canary"   // weighted split between old and new
	StagePromoted = "promoted" // all traffic on the replacement, old model as fallback
)

// CanaryHealth is measured from gateway logs for requests the rule routed.
type CanaryHealth struct {
	Since        time.Time `json:"since"`
	OldRequests  int       `json:"old_requests"`
	NewRequests  int       `json:"new_requests"`
	OldSuccess   float64   `json:"old_success_rate"`
	NewSuccess   float64   `json:"new_success_rate"`
	OldLatencyMs float64   `json:"old_latency_ms"`
	NewLatencyMs float64   `json:"new_latency_ms"`
	Fallbacks    int       `json:"fallbacks"`
	CheckedAt    time.Time `json:"checked_at"`
}

// Migration is the state of one model's cutover on the gateway.
type Migration struct {
	Model       string        `json:"model"`       // provider/model being retired
	Replacement string        `json:"replacement"` // provider/model taking over
	Stage       string        `json:"stage"`
	CanaryPct   int           `json:"canary_pct"`
	RuleID      string        `json:"rule_id,omitempty"`
	RuleName    string        `json:"rule_name,omitempty"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Health      *CanaryHealth `json:"health,omitempty"`
	History     []Change      `json:"history,omitempty"`
	GuardNote   string        `json:"guard_note,omitempty"`
}

// Event is one line in the activity feed; alert-worthy events are also sent
// to the configured webhook.
type Event struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Level   string    `json:"level"` // info, warn, critical
	Model   string    `json:"model,omitempty"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail,omitempty"`
	Alerted bool      `json:"alerted"`
}
