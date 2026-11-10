package evidence

import (
	"regexp"
	"strings"
	"unicode"
)

// Mention kinds, strongest first.
const (
	MentionExact  = "exact"  // the text names this exact model identifier
	MentionFamily = "family" // the text names a snapshot, alias or display name of the same model family
	MentionNone   = "none"
)

// Canon reduces text to lowercase words and digits separated by single
// spaces. Punctuation, markdown and whitespace differences disappear; every
// word and number stays. Quote grounding compares canonical forms, so a quote
// survives reformatting but not a changed word or date.
func Canon(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// snapshotSuffix matches the tail that turns a model alias into a dated or
// versioned snapshot of the same model: -2024-05-13, -20240229, -0613, -001,
// -latest, -preview, -v2:0, @20240229.
var snapshotSuffix = regexp.MustCompile(`(?i)^[-.@:_](?:20\d{2}-\d{2}-\d{2}|20\d{6}|\d{3,4}|latest|preview(?:[-.]\d[\w.-]*)?|exp(?:[-.]\d[\w.-]*)?|v\d+(?:[:.]\d+)*)(?:[-.:](?:v\d+(?:[:.]\d+)*|preview|latest))?$`)

func isIDChar(r byte) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == ':' || r == '@'
}

func isIDStart(r byte) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// BaseModel strips a snapshot suffix: "claude-3-opus-20240229" -> "claude-3-opus".
// It returns the input unchanged when there is no such suffix.
func BaseModel(model string) string {
	for i := len(model) - 1; i > 0; i-- {
		c := model[i]
		if c == '-' || c == '@' || c == ':' || c == '.' || c == '_' {
			if snapshotSuffix.MatchString(model[i:]) {
				return model[:i]
			}
		}
	}
	return model
}

// idMention looks for id as a standalone identifier in text. It returns
// MentionExact when the identifier stands alone, MentionFamily when it is
// immediately extended by a snapshot suffix, and MentionNone when it is only
// ever a prefix of a different model name (gpt-4o inside gpt-4o-mini).
func idMention(text, id string) string {
	if id == "" {
		return MentionNone
	}
	lt, lid := strings.ToLower(text), strings.ToLower(id)
	best := MentionNone
	from := 0
	for {
		i := strings.Index(lt[from:], lid)
		if i < 0 {
			break
		}
		start := from + i
		end := start + len(lid)
		from = start + 1
		if start > 0 && isIDStart(lt[start-1]) {
			continue
		}
		j := end
		for j < len(lt) && isIDChar(lt[j]) {
			j++
		}
		rest := strings.TrimRight(lt[end:j], ".:-_@")
		switch {
		case rest == "":
			return MentionExact
		case snapshotSuffix.MatchString(rest):
			best = MentionFamily
		}
	}
	return best
}

// displayMention matches a human-written model name against an identifier:
// "Claude 3 Opus" for claude-3-opus-20240229, "GPT-4 Turbo" for gpt-4-turbo.
func displayMention(text, base string) bool {
	cb := Canon(base)
	if len(cb) < 5 || !strings.ContainsAny(cb, "0123456789") {
		return false // too generic to match as free text
	}
	ct := " " + Canon(text) + " "
	from := 0
	for {
		i := strings.Index(ct[from:], " "+cb+" ")
		if i < 0 {
			return false
		}
		after := ct[from+i+len(cb)+2:]
		next, _, _ := strings.Cut(after, " ")
		// "gpt 4o" followed by "mini" is a different model, not this one.
		if !modelQualifier[next] {
			return true
		}
		from += i + 1
	}
}

// modelQualifier lists words that, directly after a model name, name a
// different model in the same line-up.
var modelQualifier = map[string]bool{
	"mini": true, "nano": true, "micro": true, "lite": true, "flash": true, "pro": true, "max": true,
	"turbo": true, "instant": true, "small": true, "medium": true, "large": true, "xl": true, "plus": true,
	"haiku": true, "sonnet": true, "opus": true, "instruct": true, "vision": true, "audio": true,
	"realtime": true, "search": true, "thinking": true, "fast": true, "high": true, "low": true,
	"16k": true, "32k": true, "100k": true, "200k": true, "codex": true, "embedding": true,
}

// Mention reports how strongly text refers to model (the bare model name,
// without the provider prefix).
func Mention(text, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || text == "" {
		return MentionNone
	}
	if k := idMention(text, model); k != MentionNone {
		return k
	}
	base := BaseModel(model)
	if base != model && idMention(text, base) == MentionExact {
		return MentionFamily
	}
	if displayMention(text, base) {
		return MentionFamily
	}
	return MentionNone
}

// Passage is a slice of a source page that mentions the model.
type Passage struct {
	Text    string
	Mention string // strongest mention kind inside the passage
}

// PassageOptions tunes passage extraction.
type PassageOptions struct {
	Context     int // prose lines kept on each side of a mentioning line
	MaxPassages int
	MaxChars    int // per passage
	HeadingSpan int // how far up to look for the governing heading
}

// DefaultPassageOptions suits provider documentation pages.
var DefaultPassageOptions = PassageOptions{Context: 2, MaxPassages: 8, MaxChars: 1600, HeadingSpan: 40}

func isTableRow(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "|") }

func isHeading(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "#") }

// Passages cuts a page down to the parts that mention the model. Provider
// deprecation pages are long tables covering dozens of models, so sending the
// whole page to an analyst model wastes tokens and invites it to read the
// wrong row. For a table row Heimdall keeps the row, the table header and the
// nearest heading; for prose it keeps a few lines of context.
func Passages(page, model string, opt PassageOptions) []Passage {
	if opt.MaxPassages == 0 {
		opt = DefaultPassageOptions
	}
	lines := strings.Split(strings.ReplaceAll(page, "\r\n", "\n"), "\n")
	type span struct {
		lo, hi  int
		extra   []int // header and heading lines outside [lo,hi]
		mention string
	}
	var spans []span
	for i, line := range lines {
		k := Mention(line, model)
		if k == MentionNone {
			continue
		}
		s := span{lo: i, hi: i, mention: k}
		if isTableRow(line) {
			// table header: first rows of the contiguous table block
			top := i
			for top > 0 && isTableRow(lines[top-1]) {
				top--
			}
			if top < i {
				s.extra = append(s.extra, top)
				if top+1 < i && strings.Contains(lines[top+1], "---") {
					s.extra = append(s.extra, top+1)
				}
			}
			for h := top - 1; h >= 0 && h >= top-opt.HeadingSpan; h-- {
				if isHeading(lines[h]) {
					s.extra = append([]int{h}, s.extra...)
					break
				}
			}
		} else {
			s.lo = max(0, i-opt.Context)
			s.hi = min(len(lines)-1, i+opt.Context)
			for h := s.lo - 1; h >= 0 && h >= i-opt.HeadingSpan; h-- {
				if isHeading(lines[h]) {
					s.extra = append(s.extra, h)
					break
				}
			}
		}
		// merge with the previous span when they touch
		if n := len(spans); n > 0 && s.lo <= spans[n-1].hi+1 {
			p := &spans[n-1]
			p.hi = max(p.hi, s.hi)
			if s.mention == MentionExact {
				p.mention = MentionExact
			}
			continue
		}
		spans = append(spans, s)
	}

	// exact mentions first, then document order
	var ordered []span
	for _, s := range spans {
		if s.mention == MentionExact {
			ordered = append(ordered, s)
		}
	}
	for _, s := range spans {
		if s.mention != MentionExact {
			ordered = append(ordered, s)
		}
	}

	var out []Passage
	for _, s := range ordered {
		if len(out) >= opt.MaxPassages {
			break
		}
		var b strings.Builder
		for _, e := range s.extra {
			if e < s.lo || e > s.hi {
				b.WriteString(strings.TrimRight(lines[e], " \t"))
				b.WriteByte('\n')
			}
		}
		for i := s.lo; i <= s.hi; i++ {
			if t := strings.TrimRight(lines[i], " \t"); strings.TrimSpace(t) != "" {
				b.WriteString(t)
				b.WriteByte('\n')
			}
		}
		text := strings.TrimSpace(b.String())
		if len(text) > opt.MaxChars {
			text = text[:opt.MaxChars]
		}
		if text != "" {
			out = append(out, Passage{Text: text, Mention: s.mention})
		}
	}
	return out
}

// MinQuoteChars is the shortest canonical quote Heimdall accepts. Shorter
// strings match by accident.
const MinQuoteChars = 12

// Grounded reports whether quote appears in source, comparing canonical
// forms. This is the rule that keeps an analyst model from inventing
// evidence: a verdict stands only on text that was actually retrieved.
func Grounded(quote, source string) bool {
	cq := Canon(quote)
	if len(cq) < MinQuoteChars {
		return false
	}
	return strings.Contains(Canon(source), cq)
}
