package cutover

import (
	"math"
	"testing"

	"github.com/angarkartanmay-ops/globalaihack/internal/core"
)

func TestBuildRuleStages(t *testing.T) {
	const old, repl = "aurora/aurora-large-1", "aurora/aurora-large-2"

	r, err := BuildRule(old, repl, core.StageArmed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.CELExpression != `provider == "aurora" && model == "aurora-large-1"` {
		t.Errorf("expression = %s", r.CELExpression)
	}
	if len(r.Targets) != 1 || r.Targets[0].Model != "aurora-large-1" || r.Targets[0].Weight != 1 || len(r.Fallbacks) != 1 || r.Fallbacks[0] != repl {
		t.Errorf("armed rule should keep the old model primary with the replacement as fallback: %+v", r)
	}

	r, err = BuildRule(old, repl, core.StageCanary, 30)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, tg := range r.Targets {
		sum += tg.Weight
	}
	if len(r.Targets) != 2 || math.Abs(sum-1) > 1e-9 || r.Targets[1].Model != "aurora-large-2" || math.Abs(r.Targets[1].Weight-0.3) > 1e-9 {
		t.Errorf("canary weights must sum to 1 with 30%% on the replacement: %+v", r.Targets)
	}

	r, err = BuildRule(old, repl, core.StagePromoted, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Targets) != 1 || r.Targets[0].Model != "aurora-large-2" || r.Fallbacks[0] != old {
		t.Errorf("promoted rule should send everything to the replacement with the old model as fallback: %+v", r)
	}
}

func TestBuildRuleRejectsBadInput(t *testing.T) {
	bad := []struct {
		model, repl, stage string
		pct                int
	}{
		{"aurora/a", "aurora/a", core.StageArmed, 0},
		{"a", "aurora/b", core.StageArmed, 0},
		{"aurora/a", "b", core.StageArmed, 0},
		{"aurora/a", "aurora/b", core.StageCanary, 0},
		{"aurora/a", "aurora/b", core.StageCanary, 100},
		{"aurora/a", "aurora/b", "sideways", 0},
	}
	for _, c := range bad {
		if _, err := BuildRule(c.model, c.repl, c.stage, c.pct); err == nil {
			t.Errorf("BuildRule(%q, %q, %q, %d) should fail", c.model, c.repl, c.stage, c.pct)
		}
	}
}

func TestRuleNameIsSafeAndOwned(t *testing.T) {
	if got := RuleName("openai/gpt-4o:2024@x"); got != "heimdall-openai-gpt-4o-2024-x" {
		t.Errorf("RuleName = %q", got)
	}
}

func TestGuard(t *testing.T) {
	if tripped, _ := Guard(&core.CanaryHealth{NewRequests: 5, NewSuccess: 0}, 20); tripped {
		t.Error("guard must not trip on a handful of requests")
	}
	if tripped, _ := Guard(&core.CanaryHealth{NewRequests: 40, NewSuccess: 60, OldRequests: 40, OldSuccess: 99}, 20); !tripped {
		t.Error("guard should trip when the replacement fails far more than the current model")
	}
	if tripped, _ := Guard(&core.CanaryHealth{NewRequests: 40, NewSuccess: 98, OldRequests: 40, OldSuccess: 99}, 20); tripped {
		t.Error("guard must not trip on a healthy canary")
	}
}
