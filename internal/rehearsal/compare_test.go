package rehearsal

import (
	"testing"

	"github.com/angarkartanmay-ops/globalaihack/internal/core"
)

func failed(checks []core.Check) string {
	if c, ok := HardFailure(checks); ok {
		return c.Name
	}
	return ""
}

func TestCompareToolCalls(t *testing.T) {
	base := core.Output{ToolCalls: []core.ToolCall{{Name: "lookup_order", Arguments: `{"order_id": "48213"}`}}}
	cases := []struct {
		name string
		cand core.Output
		want string
	}{
		{"same call, different spacing", core.Output{ToolCalls: []core.ToolCall{{Name: "lookup_order", Arguments: `{"order_id":"48213"}`}}}, ""},
		{"string became a number", core.Output{ToolCalls: []core.ToolCall{{Name: "lookup_order", Arguments: `{"order_id": 48213}`}}}, CheckToolCalls},
		{"different tool", core.Output{ToolCalls: []core.ToolCall{{Name: "track_parcel", Arguments: `{"order_id": "48213"}`}}}, CheckToolCalls},
		{"answered in prose instead", core.Output{Content: "Could you confirm your email?"}, CheckToolCalls},
		{"arguments are not JSON", core.Output{ToolCalls: []core.ToolCall{{Name: "lookup_order", Arguments: `order 48213`}}}, CheckToolCalls},
	}
	for _, c := range cases {
		if got := failed(Compare(base, c.cand, false)); got != c.want {
			t.Errorf("%s: failed check = %q, want %q", c.name, got, c.want)
		}
	}
	// a candidate that starts calling tools where the baseline answered is a change too
	if got := failed(Compare(core.Output{Content: "Your order shipped."}, base, false)); got != CheckToolCalls {
		t.Errorf("unexpected tool call: failed check = %q", got)
	}
}

func TestCompareJSON(t *testing.T) {
	base := core.Output{Content: `{"order_id": "48213", "intent": "refund_request", "urgency": "normal"}`}
	cases := []struct {
		name, cand, want string
	}{
		{"same object, reordered", `{"urgency":"normal","intent":"refund_request","order_id":"48213"}`, ""},
		{"markdown fence", "```json\n{\"order_id\": \"48213\", \"intent\": \"refund_request\", \"urgency\": \"normal\"}\n```", CheckJSON},
		{"extra key", `{"order_id": "48213", "intent": "refund_request", "urgency": "normal", "confidence": 0.9}`, CheckJSON},
		{"prose", `The order id is 48213.`, CheckJSON},
	}
	for _, c := range cases {
		if got := failed(Compare(base, core.Output{Content: c.cand}, true)); got != c.want {
			t.Errorf("%s: failed check = %q, want %q", c.name, got, c.want)
		}
	}
	// plain prose on both sides is not held to JSON rules
	if got := failed(Compare(core.Output{Content: "Hello there."}, core.Output{Content: "Hi there."}, false)); got != "" {
		t.Errorf("prose comparison failed check %q", got)
	}
}

func TestCompareRefusalTruncationAndErrors(t *testing.T) {
	base := core.Output{Content: "Open the tracking page and enter your tracking number.", FinishReason: "stop"}
	if got := failed(Compare(base, core.Output{Content: "I'm sorry, but I can't help with that request."}, false)); got != CheckRefusal {
		t.Errorf("refusal: failed check = %q", got)
	}
	if got := failed(Compare(base, core.Output{Content: "Open the tracking page and", FinishReason: "length"}, false)); got != CheckTruncated {
		t.Errorf("truncation: failed check = %q", got)
	}
	if got := failed(Compare(base, core.Output{Error: "bifrost 404 model_not_found"}, false)); got != CheckCompleted {
		t.Errorf("error: failed check = %q", got)
	}
	// a baseline that itself refused does not make the candidate's refusal a regression
	refused := core.Output{Content: "I cannot help with that."}
	if got := failed(Compare(refused, refused, false)); got != "" {
		t.Errorf("both refused: failed check = %q", got)
	}
}

func TestTriviallyEquivalent(t *testing.T) {
	if !TriviallyEquivalent(core.Output{Content: "Hello, World!"}, core.Output{Content: "hello world"}) {
		t.Error("text differing only in case and punctuation should be equivalent")
	}
	if !TriviallyEquivalent(core.Output{Content: `{"a": 1, "b": "X"}`}, core.Output{Content: `{"b":"x","a":1}`}) {
		t.Error("equal JSON should be equivalent")
	}
	if TriviallyEquivalent(core.Output{Content: "Use v4."}, core.Output{Content: "Use v5."}) {
		t.Error("different facts must not be trivially equivalent")
	}
}

func TestSummarizeVerdicts(t *testing.T) {
	mk := func(outcomes ...string) core.Rehearsal {
		r := core.Rehearsal{}
		for _, o := range outcomes {
			r.Samples = append(r.Samples, core.Sample{Outcome: o, Baseline: core.Output{Cost: 2, LatencyMs: 100}, Candidate: core.Output{Cost: 1, LatencyMs: 50}})
		}
		summarize(&r)
		return r
	}
	all := make([]string, 20)
	for i := range all {
		all[i] = core.OutcomeEquivalent
	}
	if r := mk(all...); r.Verdict != core.ReadyYes || r.Score != 100 || r.CostDeltaPct != -50 || r.LatencyDelta != -50 {
		t.Errorf("clean run: %+v", r)
	}
	all[0] = core.OutcomeRegression
	if r := mk(all...); r.Verdict != core.ReadyCaveats {
		t.Errorf("one regression in twenty should be ready with caveats, got %s (%.0f)", r.Verdict, r.Score)
	}
	for i := 0; i < 6; i++ {
		all[i] = core.OutcomeRegression
	}
	if r := mk(all...); r.Verdict != core.ReadyNo {
		t.Errorf("six regressions in twenty should be not ready, got %s", r.Verdict)
	}
}
