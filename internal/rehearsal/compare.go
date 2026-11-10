package rehearsal

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/angarkartanmay-ops/globalaihack/internal/core"
	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
)

// Deterministic checks. These catch the regressions that break applications
// outright (a tool call that changed shape, JSON that no longer parses, a
// truncated or refused answer) without spending a model call, and without
// asking a model to notice something a parser can prove.
const (
	CheckCompleted = "completed"
	CheckToolCalls = "tool_calls"
	CheckJSON      = "json_output"
	CheckTruncated = "not_truncated"
	CheckRefusal   = "no_refusal"
	CheckLength    = "length"
)

// hardChecks fail a sample on their own. The rest are advisory.
var hardChecks = map[string]bool{CheckCompleted: true, CheckToolCalls: true, CheckJSON: true, CheckTruncated: true, CheckRefusal: true}

var refusalRe = regexp.MustCompile(`(?i)^\W*(i['’]?m sorry|i am sorry|sorry,? (but )?i|i can(no|['’])t (help|assist|comply|provide|do)|i cannot (help|assist|comply|provide|do)|i['’]?m (not able|unable) to|i am (not able|unable) to|as an ai\b|i won['’]?t be able to)`)

// IsRefusal reports whether text opens like a refusal.
func IsRefusal(text string) bool { return refusalRe.MatchString(strings.TrimSpace(text)) }

// parseJSONStrict parses s as a JSON object or array with nothing around it.
func parseJSONStrict(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

func topKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// looseEqual compares decoded JSON values, ignoring case and punctuation in
// strings so that "Shipped" and "shipped." count as the same value.
func looseEqual(a, b any) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && evidence.Canon(x) == evidence.Canon(y)
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !looseEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !looseEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func describeCalls(calls []core.ToolCall) string {
	if len(calls) == 0 {
		return "no tool call"
	}
	parts := make([]string, len(calls))
	for i, c := range calls {
		parts[i] = c.Name + "(" + compact(c.Arguments, 80) + ")"
	}
	return strings.Join(parts, ", ")
}

func compact(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// compareToolCalls requires the same tools, in the same order, with
// arguments that decode to the same values.
func compareToolCalls(b, c []core.ToolCall) (bool, string) {
	if len(b) != len(c) {
		return false, fmt.Sprintf("baseline: %s; candidate: %s", describeCalls(b), describeCalls(c))
	}
	for i := range b {
		if b[i].Name != c[i].Name {
			return false, fmt.Sprintf("baseline called %s; candidate called %s", b[i].Name, c[i].Name)
		}
		var ba, ca any
		berr := json.Unmarshal([]byte(orEmptyObject(b[i].Arguments)), &ba)
		cerr := json.Unmarshal([]byte(orEmptyObject(c[i].Arguments)), &ca)
		if cerr != nil {
			return false, fmt.Sprintf("candidate's arguments for %s are not valid JSON: %s", c[i].Name, compact(c[i].Arguments, 80))
		}
		if berr == nil && !looseEqual(ba, ca) {
			return false, fmt.Sprintf("%s arguments differ. baseline: %s; candidate: %s", b[i].Name, compact(b[i].Arguments, 80), compact(c[i].Arguments, 80))
		}
	}
	return true, ""
}

func orEmptyObject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// Compare runs every deterministic check that applies to this sample.
// wantsJSON is true when the request asked for JSON output.
func Compare(b, c core.Output, wantsJSON bool) []core.Check {
	var checks []core.Check
	if c.Error != "" {
		return []core.Check{{Name: CheckCompleted, Pass: false, Detail: c.Error}}
	}
	checks = append(checks, core.Check{Name: CheckCompleted, Pass: true})

	if len(b.ToolCalls) > 0 || len(c.ToolCalls) > 0 {
		ok, detail := compareToolCalls(b.ToolCalls, c.ToolCalls)
		checks = append(checks, core.Check{Name: CheckToolCalls, Pass: ok, Detail: detail})
	}

	bv, bIsJSON := parseJSONStrict(b.Content)
	if (wantsJSON || bIsJSON) && strings.TrimSpace(b.Content) != "" {
		cv, cIsJSON := parseJSONStrict(c.Content)
		switch {
		case !cIsJSON && strings.Contains(c.Content, "```"):
			checks = append(checks, core.Check{Name: CheckJSON, Pass: false, Detail: "candidate wrapped its JSON in a markdown code fence; a strict parser will fail on it"})
		case !cIsJSON:
			checks = append(checks, core.Check{Name: CheckJSON, Pass: false, Detail: "candidate's output does not parse as JSON"})
		case bIsJSON && !reflect.DeepEqual(topKeys(bv), topKeys(cv)):
			checks = append(checks, core.Check{Name: CheckJSON, Pass: false,
				Detail: fmt.Sprintf("top-level keys differ. baseline: %v; candidate: %v", topKeys(bv), topKeys(cv))})
		default:
			checks = append(checks, core.Check{Name: CheckJSON, Pass: true})
		}
	}

	if c.FinishReason == "length" && b.FinishReason != "length" {
		checks = append(checks, core.Check{Name: CheckTruncated, Pass: false, Detail: "candidate hit the output token limit; baseline finished normally"})
	} else {
		checks = append(checks, core.Check{Name: CheckTruncated, Pass: true})
	}

	if IsRefusal(c.Content) && !IsRefusal(b.Content) {
		checks = append(checks, core.Check{Name: CheckRefusal, Pass: false, Detail: "candidate refused a request the baseline answered"})
	} else {
		checks = append(checks, core.Check{Name: CheckRefusal, Pass: true})
	}

	if lb, lc := len(strings.TrimSpace(b.Content)), len(strings.TrimSpace(c.Content)); lb >= 80 {
		ratio := float64(lc) / float64(lb)
		if ratio < 0.3 || ratio > 3 {
			checks = append(checks, core.Check{Name: CheckLength, Pass: false, Detail: fmt.Sprintf("candidate's answer is %.1fx the length of the baseline's", ratio)})
		}
	}
	return checks
}

// HardFailure returns the first failed hard check, if any.
func HardFailure(checks []core.Check) (core.Check, bool) {
	for _, c := range checks {
		if !c.Pass && hardChecks[c.Name] {
			return c, true
		}
	}
	return core.Check{}, false
}

// TriviallyEquivalent reports whether the two outputs are the same without
// needing a judge: identical text, identical decoded JSON, or matching tool
// calls with no text.
func TriviallyEquivalent(b, c core.Output) bool {
	bt, ct := strings.TrimSpace(b.Content), strings.TrimSpace(c.Content)
	if bt == "" && ct == "" {
		return len(b.ToolCalls) > 0 || len(c.ToolCalls) > 0
	}
	if evidence.Canon(bt) == evidence.Canon(ct) {
		return true
	}
	bv, bok := parseJSONStrict(bt)
	cv, cok := parseJSONStrict(ct)
	return bok && cok && looseEqual(bv, cv)
}
