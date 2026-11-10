// Package cutover moves traffic from a retiring model to its replacement by
// managing Bifrost routing rules.
//
// Heimdall never proxies or rewrites requests itself. A migration is one
// routing rule on the gateway, matched on the retiring model, that Heimdall
// creates and updates through the public /api/routing/rules API:
//
//	armed     old model 100%, replacement as fallback   (survive the shutdown)
//	canary    weighted split between old and new        (prove it on live traffic)
//	promoted  replacement 100%, old model as fallback   (done; old is the net)
//
// Removing the rule returns the gateway to exactly its previous behaviour.
package cutover

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
)

// RulePrefix marks the routing rules Heimdall owns. It never edits others.
const RulePrefix = "heimdall-"

// Gateway is the part of the Bifrost client cutover uses.
type Gateway interface {
	ListRules(ctx context.Context) ([]bifrost.Rule, error)
	CreateRule(ctx context.Context, r bifrost.Rule) (*bifrost.Rule, error)
	UpdateRule(ctx context.Context, id string, r bifrost.Rule) (*bifrost.Rule, error)
	DeleteRule(ctx context.Context, id string) error
	Logs(ctx context.Context, f bifrost.LogFilter) (*bifrost.LogPage, error)
}

// Manager applies migrations to the gateway.
type Manager struct {
	GW           Gateway
	Now          func() time.Time
	BasePriority int // Heimdall's rules take the first free priority from here up
}

// New returns a manager.
func New(gw Gateway) *Manager { return &Manager{GW: gw, Now: time.Now, BasePriority: 100} }

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// RuleName is the routing rule name for a model's migration.
func RuleName(model string) string {
	return RulePrefix + strings.Trim(unsafeName.ReplaceAllString(model, "-"), "-")
}

// Expression is the CEL match for requests addressed to a model.
func Expression(m core.ModelRef) string {
	return fmt.Sprintf("provider == %q && model == %q", m.Provider, m.Model)
}

// BuildRule returns the routing rule for a migration stage.
func BuildRule(model, replacement, stage string, canaryPct int) (bifrost.Rule, error) {
	old, repl := core.ParseModelRef(model), core.ParseModelRef(replacement)
	if old.Provider == "" || old.Model == "" || repl.Provider == "" || repl.Model == "" {
		return bifrost.Rule{}, errors.New("model and replacement must both be provider/model")
	}
	if old == repl {
		return bifrost.Rule{}, errors.New("replacement is the same model")
	}
	r := bifrost.Rule{Name: RuleName(model), Enabled: true, CELExpression: Expression(old), Scope: "global"}
	oldT := bifrost.Target{Provider: old.Provider, Model: old.Model}
	newT := bifrost.Target{Provider: repl.Provider, Model: repl.Model}
	switch stage {
	case core.StageArmed:
		oldT.Weight = 1
		r.Targets = []bifrost.Target{oldT}
		r.Fallbacks = []string{replacement}
		r.Description = fmt.Sprintf("Heimdall safety net: %s keeps serving; if it fails, %s takes the request.", model, replacement)
	case core.StageCanary:
		if canaryPct < 1 || canaryPct > 99 {
			return bifrost.Rule{}, errors.New("canary percentage must be between 1 and 99")
		}
		newT.Weight = float64(canaryPct) / 100
		oldT.Weight = float64(100-canaryPct) / 100
		r.Targets = []bifrost.Target{oldT, newT}
		// Whichever side a request lands on, the other side is its net.
		r.Fallbacks = []string{replacement, model}
		r.Description = fmt.Sprintf("Heimdall canary: %d%% of %s traffic is served by %s.", canaryPct, model, replacement)
	case core.StagePromoted:
		newT.Weight = 1
		r.Targets = []bifrost.Target{newT}
		r.Fallbacks = []string{model}
		r.Description = fmt.Sprintf("Heimdall cutover: requests for %s are served by %s.", model, replacement)
	default:
		return bifrost.Rule{}, fmt.Errorf("unknown stage %q", stage)
	}
	return r, nil
}

// Apply puts a migration into the given stage on the gateway.
func (m *Manager) Apply(ctx context.Context, mig core.Migration, stage string, canaryPct int) (core.Migration, error) {
	rule, err := BuildRule(mig.Model, mig.Replacement, stage, canaryPct)
	if err != nil {
		return mig, err
	}
	existing, err := m.GW.ListRules(ctx)
	if err != nil {
		return mig, fmt.Errorf("listing routing rules: %w", err)
	}
	var current *bifrost.Rule
	used := map[int]bool{}
	for i := range existing {
		e := &existing[i]
		used[e.Priority] = true
		if e.Name == rule.Name || (mig.RuleID != "" && e.ID == mig.RuleID) {
			current = e
		}
	}
	var saved *bifrost.Rule
	if current != nil {
		rule.Priority = current.Priority
		saved, err = m.GW.UpdateRule(ctx, current.ID, rule)
		if err == nil && saved.ID == "" {
			saved.ID = current.ID
		}
	} else {
		// Priorities must be unique within a scope on the gateway.
		rule.Priority = m.BasePriority
		for used[rule.Priority] {
			rule.Priority++
		}
		saved, err = m.GW.CreateRule(ctx, rule)
	}
	if err != nil {
		return mig, fmt.Errorf("writing routing rule: %w", err)
	}
	now := m.Now().UTC()
	from := mig.Stage
	if from == "" {
		from = core.StageNone
	}
	to := stage
	if stage == core.StageCanary {
		to = fmt.Sprintf("canary %d%%", canaryPct)
	}
	if mig.Stage == core.StageCanary {
		from = fmt.Sprintf("canary %d%%", mig.CanaryPct)
	}
	mig.History = append(mig.History, core.Change{At: now, Field: "stage", From: from, To: to})
	mig.Stage, mig.RuleID, mig.RuleName, mig.UpdatedAt = stage, saved.ID, rule.Name, now
	mig.CanaryPct = 0
	if stage == core.StageCanary {
		mig.CanaryPct = canaryPct
	}
	mig.Health, mig.GuardNote = nil, ""
	return mig, nil
}

// Rollback removes the migration's routing rule.
func (m *Manager) Rollback(ctx context.Context, mig core.Migration) (core.Migration, error) {
	id := mig.RuleID
	if id == "" {
		rules, err := m.GW.ListRules(ctx)
		if err != nil {
			return mig, err
		}
		for _, r := range rules {
			if r.Name == RuleName(mig.Model) {
				id = r.ID
			}
		}
	}
	if id != "" {
		if err := m.GW.DeleteRule(ctx, id); err != nil {
			return mig, fmt.Errorf("deleting routing rule: %w", err)
		}
	}
	now := m.Now().UTC()
	mig.History = append(mig.History, core.Change{At: now, Field: "stage", From: mig.Stage, To: core.StageNone})
	mig.Stage, mig.RuleID, mig.CanaryPct, mig.UpdatedAt, mig.Health, mig.GuardNote = core.StageNone, "", 0, now, nil, ""
	return mig, nil
}

// Health measures how the two models are doing on the traffic this
// migration's rule has routed since it entered its current stage. It reads
// gateway logs filtered by the rule, so it reflects real requests only.
func (m *Manager) Health(ctx context.Context, mig core.Migration) (*core.CanaryHealth, error) {
	if mig.RuleID == "" {
		return nil, nil
	}
	h := &core.CanaryHealth{Since: mig.UpdatedAt, CheckedAt: m.Now().UTC()}
	old, repl := core.ParseModelRef(mig.Model), core.ParseModelRef(mig.Replacement)
	var oldOK, newOK int
	var oldLat, newLat float64
	// Only requests routed since the current stage began count; earlier
	// stages used the same rule with different targets.
	f := bifrost.LogFilter{RoutingRuleIDs: []string{mig.RuleID}, Start: mig.UpdatedAt, Limit: 200}
	for page := 0; page < 5; page++ {
		f.Offset = page * 200
		p, err := m.GW.Logs(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, l := range p.Logs {
			// The gateway's time filter has one-second resolution.
			if l.Timestamp.Before(mig.UpdatedAt) {
				continue
			}
			if l.FallbackIndex > 0 {
				h.Fallbacks++
			}
			ok := l.Status == "success"
			switch {
			case l.Provider == repl.Provider && l.Model == repl.Model:
				h.NewRequests++
				if ok {
					newOK++
					newLat += l.Latency
				}
			case l.Provider == old.Provider && l.Model == old.Model:
				h.OldRequests++
				if ok {
					oldOK++
					oldLat += l.Latency
				}
			}
		}
		if len(p.Logs) < 200 {
			break
		}
	}
	if h.OldRequests > 0 {
		h.OldSuccess = 100 * float64(oldOK) / float64(h.OldRequests)
	}
	if h.NewRequests > 0 {
		h.NewSuccess = 100 * float64(newOK) / float64(h.NewRequests)
	}
	if oldOK > 0 {
		h.OldLatencyMs = oldLat / float64(oldOK)
	}
	if newOK > 0 {
		h.NewLatencyMs = newLat / float64(newOK)
	}
	return h, nil
}

// Guard looks at canary health and says whether the replacement is failing
// noticeably more than the model it replaces.
func Guard(h *core.CanaryHealth, minRequests int) (tripped bool, note string) {
	if h == nil || h.NewRequests < minRequests {
		return false, ""
	}
	if h.NewSuccess < 80 || (h.OldRequests >= minRequests && h.NewSuccess < h.OldSuccess-10) {
		return true, fmt.Sprintf("The replacement is succeeding on %.0f%% of %d requests, against %.0f%% for the current model.", h.NewSuccess, h.NewRequests, h.OldSuccess)
	}
	return false, ""
}
