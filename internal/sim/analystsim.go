package sim

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
)

// The simulated analyst is a rule-based stand-in for the model that reads
// evidence in live mode. It answers the same prompts with the same JSON
// shapes, by pattern-matching the excerpts it is given. It knows nothing the
// excerpts do not say, which is the property the real prompts demand of a
// real model. It exists for tests and for the keyless demonstration; it is
// not a language model and is labelled as simulated wherever it appears.

var taskRe = regexp.MustCompile(`\[heimdall-task: ([a-z_]+)\]`)

func taskOf(system string) string {
	if m := taskRe.FindStringSubmatch(system); m != nil {
		return m[1]
	}
	return ""
}

func (w *World) analyst(task, user string) string {
	var out any
	switch task {
	case "sunset_extract":
		out = w.simSunset(user)
	case "replay_judge":
		out = simJudge(user)
	case "claims_extract":
		out = simClaims(user)
	case "claim_assess":
		out = simAssess(user)
	case "answer_rewrite":
		return simRewrite(user)
	default:
		out = map[string]any{"error": "unknown task " + task}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// ---------------------------------------------------------------------------
// sunset_extract
// ---------------------------------------------------------------------------

var (
	excerptRe  = regexp.MustCompile(`(?s)<excerpt id="([^"]+)" url="([^"]*)" mention="([^"]*)">\n(.*?)\n</excerpt>`)
	targetRe   = regexp.MustCompile(`(?m)^Target model: (.+)$`)
	modelLike  = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:-[a-z0-9]+)+\b`)
	activeRe   = regexp.MustCompile(`(?i)no retirement|generally available|current flagship`)
	lifecycle  = regexp.MustCompile(`(?i)shut down|shutdown|retire|stops working|end of life|deprecat`)
	paraphrase = strings.NewReplacer("stops working on", "will stop working on", "Support confirmed to me that ", "")
)

func cells(row string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func (w *World) simSunset(user string) map[string]any {
	target := ""
	if m := targetRe.FindStringSubmatch(user); m != nil {
		target = strings.TrimSpace(m[1])
	}
	notices := []map[string]any{}
	for _, m := range excerptRe.FindAllStringSubmatch(user, -1) {
		id, url, text := m[1], m[2], m[4]
		units := sentences(text)
		// A table's header tells which column holds the shutdown date.
		var header []string
		for _, u := range units {
			if strings.HasPrefix(u, "|") && !strings.HasPrefix(u, "|---") {
				header = cells(u)
				break
			}
		}
		for i, u := range units {
			kind := evidence.Mention(u, target)
			if kind == evidence.MentionNone || strings.HasPrefix(u, "#") {
				continue
			}
			applies := "exact"
			if kind != evidence.MentionExact {
				applies = "alias"
			}
			n := map[string]any{"passage": id, "applies": applies, "quote": u, "shutdown_date": nil, "replacement": nil}
			switch {
			case strings.HasPrefix(u, "|") && header != nil && len(cells(u)) == len(header):
				row := cells(u)
				status := ""
				for c, h := range header {
					lh := strings.ToLower(h)
					switch {
					case strings.Contains(lh, "shutdown") || strings.Contains(lh, "retire"):
						if ds := evidence.FindDates(row[c]); len(ds) > 0 {
							n["shutdown_date"] = ds[0].ISO
							status = "deprecated"
						}
					case strings.Contains(lh, "replacement"):
						n["replacement"] = row[c]
					case strings.Contains(lh, "status") && activeRe.MatchString(row[c]):
						status = "active"
					}
				}
				if status == "" {
					continue
				}
				n["status"] = status
			case activeRe.MatchString(u):
				n["status"] = "active"
			case lifecycle.MatchString(u):
				n["status"] = "deprecated"
				if ds := evidence.FindDates(u); len(ds) > 0 {
					n["shutdown_date"] = ds[len(ds)-1].ISO
				}
				// the replacement is named in this sentence or the next
				scope := u
				if i+1 < len(units) {
					scope += " " + units[i+1]
				}
				for _, cand := range modelLike.FindAllString(scope, -1) {
					if evidence.Mention(cand, target) == evidence.MentionNone && cand != evidence.BaseModel(target) {
						n["replacement"] = cand
						break
					}
				}
			default:
				continue
			}
			// Injected fault: on forum posts the simulated analyst rewords
			// its quote instead of copying it, as real models sometimes do.
			// Heimdall must notice and discard the notice.
			if w.Faults && strings.Contains(url, "devforum") {
				n["quote"] = paraphrase.Replace(u)
			}
			notices = append(notices, n)
		}
	}
	return map[string]any{"notices": notices, "reasoning": fmt.Sprintf("%d statement(s) in the excerpts concern the target model's lifecycle.", len(notices))}
}

// ---------------------------------------------------------------------------
// Values: the kinds of fact that go stale.
// ---------------------------------------------------------------------------

var valueRes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"version", regexp.MustCompile(`\bv\d+(?:\.\d+)*\b`)},
	{"price", regexp.MustCompile(`\$\d+(?:\.\d{1,2})?`)},
	{"duration", regexp.MustCompile(`\b\d+ to \d+ (?:business )?days\b`)},
	{"name", regexp.MustCompile(`\b[A-Z][a-z]+ [A-Z][a-z]+\b`)},
}

var notNames = map[string]bool{"Chief Executive": true, "Executive Officer": true, "Northpost Api": true, "Standard Delivery": true,
	"Express Delivery": true, "Weekend Delivery": true, "Shipping News": true, "New Integrations": true, "Thanks For": true}

// valuesIn returns the values of each kind in text, in order of appearance.
func valuesIn(text string) map[string][]string {
	out := map[string][]string{}
	for _, v := range valueRes {
		for _, m := range v.re.FindAllString(text, -1) {
			if v.kind == "name" && (notNames[m] || strings.HasPrefix(m, "Northpost") || strings.HasPrefix(m, "The ") || strings.HasPrefix(m, "Your ")) {
				continue
			}
			out[v.kind] = append(out[v.kind], m)
		}
	}
	return out
}

func sentenceWith(text, value string) string {
	for _, s := range sentences(text) {
		if strings.Contains(s, value) {
			return s
		}
	}
	return strings.TrimSpace(text)
}

// ---------------------------------------------------------------------------
// replay_judge
// ---------------------------------------------------------------------------

var (
	sectionRe = regexp.MustCompile(`(?s)REQUEST \(last user message\):\n(.*?)\n\nBASELINE:\n(.*?)\n\nCANDIDATE:\n(.*)$`)
	daysRe    = regexp.MustCompile(`\b\d+ days\b`)
)

func simJudge(user string) map[string]any {
	m := sectionRe.FindStringSubmatch(user)
	if m == nil {
		return map[string]any{"verdict": "acceptable", "reason": "Could not read the two answers.", "disagreements": []any{}}
	}
	request, base, cand := strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
	bv, cv := valuesIn(base), valuesIn(cand)

	disagreements := []map[string]string{}
	for _, kind := range []string{"version", "price", "duration", "name"} {
		if len(bv[kind]) > 0 && len(cv[kind]) > 0 && bv[kind][0] != cv[kind][0] {
			disagreements = append(disagreements, map[string]string{
				"topic": request, "baseline": sentenceWith(base, bv[kind][0]), "candidate": sentenceWith(cand, cv[kind][0])})
		}
	}
	// A fact the baseline gives and the candidate drops is a regression.
	for _, d := range daysRe.FindAllString(base, -1) {
		if !strings.Contains(base, "business days") && !strings.Contains(cand, d) {
			return map[string]any{"verdict": "regression", "disagreements": disagreements,
				"reason": fmt.Sprintf("The candidate leaves out a fact the baseline states (%s).", d)}
		}
	}
	if len(disagreements) > 0 {
		return map[string]any{"verdict": "acceptable", "disagreements": disagreements,
			"reason": "The answers agree except on a fact that can change over time."}
	}
	ratio := float64(len(cand)) / float64(max(1, len(base)))
	switch {
	case ratio > 1.4:
		return map[string]any{"verdict": "acceptable", "disagreements": disagreements, "reason": "Same facts; the candidate adds a greeting and a closing line."}
	case ratio < 0.75:
		return map[string]any{"verdict": "acceptable", "disagreements": disagreements, "reason": "Same facts; the candidate is shorter."}
	}
	return map[string]any{"verdict": "equivalent", "disagreements": disagreements, "reason": "Same facts and format; only the wording differs."}
}

// ---------------------------------------------------------------------------
// claims_extract
// ---------------------------------------------------------------------------

var (
	answerRe   = regexp.MustCompile(`(?s)\nAnswer:\n(.*)$`)
	questionRe = regexp.MustCompile(`(?s)\nQuestion:\n(.*?)\n\nAnswer:`)
	timeWords  = regexp.MustCompile(`(?i)\b(latest|current|currently|now|today|as of|newest|recent)\b`)
)

var categoryOf = map[string]string{"version": "version", "price": "price", "duration": "schedule", "name": "role"}

func simClaims(user string) map[string]any {
	answer, question := "", ""
	if m := answerRe.FindStringSubmatch(user); m != nil {
		answer = strings.TrimSpace(m[1])
	}
	if m := questionRe.FindStringSubmatch(user); m != nil {
		question = strings.TrimSpace(m[1])
	}
	claims := []map[string]any{}
	for i, s := range sentences(answer) {
		if len(claims) >= 12 || len(keywords(s)) < 2 || strings.HasSuffix(s, "?") {
			continue
		}
		vals := valuesIn(s)
		category, sensitive := "other", false
		for _, k := range []string{"version", "price", "duration", "name"} {
			if len(vals[k]) > 0 {
				category, sensitive = categoryOf[k], true
				break
			}
		}
		if !sensitive && timeWords.MatchString(s) {
			sensitive, category = true, "status"
		}
		c := map[string]any{"id": fmt.Sprintf("c%d", i+1), "text": s, "time_sensitive": sensitive, "category": category}
		if sensitive {
			q := strings.Join(keywords(s), " ")
			if question != "" && question != "(not provided)" {
				q = strings.Join(keywords(question), " ") + " " + q
			}
			c["query"] = q
		}
		claims = append(claims, c)
	}
	return map[string]any{"claims": claims}
}

// ---------------------------------------------------------------------------
// claim_assess
// ---------------------------------------------------------------------------

var (
	claimRe  = regexp.MustCompile(`(?m)^Claim: (.+)$`)
	resultRe = regexp.MustCompile(`(?s)<result id="([^"]+)" url="([^"]*)" published="([^"]*)" title="([^"]*)">\n(.*?)\n</result>`)
)

func simAssess(user string) map[string]any {
	unverifiable := map[string]any{"verdict": "unverifiable", "current_value": nil, "source": nil, "quote": nil,
		"reasoning": "None of the results states this fact."}
	m := claimRe.FindStringSubmatch(user)
	if m == nil {
		return unverifiable
	}
	claim := m[1]
	cvals := valuesIn(claim)
	ckeys := map[string]bool{}
	for _, k := range keywords(claim) {
		ckeys[k] = true
	}
	// the result sentence that shares the most words with the claim and
	// carries a value of the kind the claim is about
	bestScore, bestID, bestSentence, bestKind := 0, "", "", ""
	for _, r := range resultRe.FindAllStringSubmatch(user, -1) {
		for _, s := range sentences(r[5]) {
			svals := valuesIn(s)
			for _, kind := range []string{"version", "price", "duration", "name"} {
				if len(cvals[kind]) == 0 || len(svals[kind]) == 0 {
					continue
				}
				if score := overlap(ckeys, s); score > bestScore {
					bestScore, bestID, bestSentence, bestKind = score, r[1], s, kind
				}
			}
		}
	}
	if bestScore < 2 {
		return unverifiable
	}
	// Pages state the current value first; an old value follows it
	// ("changed from", "succeeding", "replacing").
	current := valuesIn(bestSentence)[bestKind][0]
	if current == cvals[bestKind][0] {
		return map[string]any{"verdict": "supported", "current_value": current, "source": bestID, "quote": bestSentence,
			"reasoning": "The result states the same value as the claim."}
	}
	return map[string]any{"verdict": "changed", "current_value": current, "source": bestID, "quote": bestSentence,
		"reasoning": fmt.Sprintf("The result gives %s where the claim says %s.", current, cvals[bestKind][0])}
}

// ---------------------------------------------------------------------------
// answer_rewrite
// ---------------------------------------------------------------------------

var (
	originalRe = regexp.MustCompile(`(?s)\nOriginal answer:\n(.*?)\n\nFindings:`)
	changedRe  = regexp.MustCompile(`(?m)^- \[(\d+)\] CHANGED: "(.*)" is no longer accurate\. Current fact: (.*)\. Source: (\S*)$`)
)

func simRewrite(user string) string {
	m := originalRe.FindStringSubmatch(user)
	if m == nil {
		return ""
	}
	answer := strings.TrimSpace(m[1])
	var sources []string
	for _, f := range changedRe.FindAllStringSubmatch(user, -1) {
		n, claim, current, url := f[1], f[2], f[3], f[4]
		for kind, vals := range valuesIn(claim) {
			if cur := valuesIn(current)[kind]; len(cur) > 0 && len(vals) > 0 && vals[0] != cur[0] {
				answer = strings.Replace(answer, vals[0], cur[0]+" ["+n+"]", 1)
			}
		}
		sources = append(sources, fmt.Sprintf("[%s] %s", n, url))
	}
	if len(sources) > 0 {
		answer += "\n\nSources:\n" + strings.Join(sources, "\n")
	}
	return answer
}
