package evidence

import (
	"strings"
	"testing"
	"time"
)

func TestFindDates(t *testing.T) {
	cases := []struct {
		in   string
		want []string // ISO:precision
	}{
		{"Shutdown on January 5, 2026.", []string{"2026-01-05:day"}},
		{"retires Jan 5th 2026", []string{"2026-01-05:day"}},
		{"retires on 5 January 2026", []string{"2026-01-05:day"}},
		{"the 3rd of March, 2027", []string{"2027-03-03:day"}},
		{"shutdown_date: 2026-12-15", []string{"2026-12-15:day"}},
		{"2026/07/01 is the date", []string{"2026-07-01:day"}},
		{"will be retired in March 2027", []string{"2027-03-01:month"}},
		{"| gpt-x | September 4, 2026 | December 3, 2026 | gpt-y |", []string{"2026-09-04:day", "2026-12-03:day"}},
		{"Sept. 30, 2026 and May 2027", []string{"2026-09-30:day", "2027-05-01:month"}},
		// ambiguous numeric orders must not become dates
		{"on 01/02/2026 we changed things", nil},
		// an impossible date is not a date at all
		{"February 30, 2026", nil},
		{"version 2026.1 released", nil},
	}
	for _, c := range cases {
		var got []string
		for _, d := range FindDates(c.in) {
			got = append(got, d.ISO+":"+d.Precision)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("FindDates(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDateIn(t *testing.T) {
	if _, ok := DateIn("shut down on December 3, 2026", "2026-12-03"); !ok {
		t.Error("exact date should be found in the quote")
	}
	if _, ok := DateIn("shut down on December 3, 2026", "2026-12-04"); ok {
		t.Error("a different day must not match")
	}
	if d, ok := DateIn("retired in March 2027", "2027-03-15"); !ok || d.Precision != "month" {
		t.Errorf("a month-precision mention should match a day in that month, got %+v %v", d, ok)
	}
	if _, ok := DateIn("no date here", "2027-03-15"); ok {
		t.Error("text without a date must not match")
	}
}

func TestDaysUntil(t *testing.T) {
	now := time.Date(2026, 11, 10, 15, 30, 0, 0, time.UTC)
	if d, ok := DaysUntil("2026-12-03", now); !ok || d != 23 {
		t.Errorf("DaysUntil = %d %v, want 23", d, ok)
	}
	if d, _ := DaysUntil("2026-11-05", now); d != -5 {
		t.Errorf("DaysUntil past = %d, want -5", d)
	}
	if _, ok := DaysUntil("soon", now); ok {
		t.Error("garbage must not parse")
	}
}

func TestMention(t *testing.T) {
	cases := []struct{ text, model, want string }{
		{"gpt-4o will be retired", "gpt-4o", MentionExact},
		{"`gpt-4o`.", "gpt-4o", MentionExact},
		// a longer model name is a different model
		{"gpt-4o-mini will be retired", "gpt-4o", MentionNone},
		{"gpt-4.1 will be retired", "gpt-4", MentionNone},
		{"gpt-4-turbo is deprecated", "gpt-4", MentionNone},
		// a dated snapshot of the alias is the same family
		{"gpt-4o-2024-05-13 will be retired", "gpt-4o", MentionFamily},
		{"gpt-4-0613 shuts down", "gpt-4", MentionFamily},
		// the alias named where traffic uses a snapshot
		{"The lumen-chat-2 family will be retired", "lumen-chat-2-0815", MentionFamily},
		{"claude-3-opus is deprecated", "claude-3-opus-20240229", MentionFamily},
		// display names
		{"Claude 3 Opus will be retired", "claude-3-opus-20240229", MentionFamily},
		{"GPT-4o mini is going away", "gpt-4o", MentionNone},
		{"Claude 3 Opus is deprecated", "claude-3-sonnet-20240229", MentionNone},
		// substring inside another identifier
		{"my-gpt-4o-proxy", "gpt-4o", MentionNone},
		{"nothing relevant", "gpt-4o", MentionNone},
		{"o1 is retired", "o1", MentionExact},
		{"The bo1ler is hot", "o1", MentionNone},
	}
	for _, c := range cases {
		if got := Mention(c.text, c.model); got != c.want {
			t.Errorf("Mention(%q, %q) = %s, want %s", c.text, c.model, got, c.want)
		}
	}
}

func TestBaseModel(t *testing.T) {
	cases := map[string]string{
		"claude-3-opus-20240229": "claude-3-opus",
		"gpt-4o-2024-05-13":      "gpt-4o",
		"gpt-4-0613":             "gpt-4",
		"gemini-1.5-pro-001":     "gemini-1.5-pro",
		"gpt-4o":                 "gpt-4o",
		"gpt-4o-mini":            "gpt-4o-mini",
		"lumen-chat-2-0815":      "lumen-chat-2",
	}
	for in, want := range cases {
		if got := BaseModel(in); got != want {
			t.Errorf("BaseModel(%q) = %q, want %q", in, got, want)
		}
	}
}

const page = `# Model deprecations

Intro paragraph that mentions nothing.

## Deprecation schedule

| Model | Deprecation announced | Shutdown date | Recommended replacement |
|---|---|---|---|
| alpha-large-1 | September 4, 2026 | December 3, 2026 | alpha-large-2 |
| alpha-mini-1 | October 17, 2026 | February 14, 2027 | alpha-mini-2 |

## Notes

We are announcing the deprecation of alpha-large-1.
It will be shut down soon.
Unrelated closing line.
Another unrelated line.
Yet another unrelated line.
`

func TestPassagesTableRowKeepsHeaderAndHeading(t *testing.T) {
	ps := Passages(page, "alpha-large-1", DefaultPassageOptions)
	if len(ps) != 2 {
		t.Fatalf("got %d passages, want 2: %+v", len(ps), ps)
	}
	row := ps[0].Text
	for _, want := range []string{"## Deprecation schedule", "| Model | Deprecation announced | Shutdown date |", "| alpha-large-1 |"} {
		if !strings.Contains(row, want) {
			t.Errorf("table passage is missing %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "alpha-mini-1") {
		t.Errorf("table passage must not carry another model's row:\n%s", row)
	}
	if ps[0].Mention != MentionExact {
		t.Errorf("mention = %s, want exact", ps[0].Mention)
	}
	if !strings.Contains(ps[1].Text, "It will be shut down soon.") || !strings.Contains(ps[1].Text, "## Notes") {
		t.Errorf("prose passage should carry context and its heading:\n%s", ps[1].Text)
	}
}

func TestPassagesNoneForUnmentionedModel(t *testing.T) {
	if ps := Passages(page, "alpha-large-2-pro", DefaultPassageOptions); len(ps) != 0 {
		t.Errorf("expected no passages, got %+v", ps)
	}
}

func TestGrounded(t *testing.T) {
	src := "| alpha-large-1 | September 4, 2026 | **December 3, 2026** | `alpha-large-2` |"
	if !Grounded("alpha-large-1 | September 4, 2026 | December 3, 2026 | alpha-large-2", src) {
		t.Error("a quote that differs only in markdown and spacing should be grounded")
	}
	if Grounded("alpha-large-1 | September 4, 2026 | December 13, 2026 | alpha-large-2", src) {
		t.Error("a quote with a changed date must not be grounded")
	}
	if Grounded("alpha-large-1 will be shut down on December 3, 2026", src) {
		t.Error("a paraphrase must not be grounded")
	}
	if Grounded("2026", src) {
		t.Error("a very short quote must not be accepted")
	}
}

func TestRegistry(t *testing.T) {
	reg := DefaultRegistry()
	if !reg.Official("openai", "https://platform.openai.com/docs/deprecations") {
		t.Error("openai docs should be official for openai")
	}
	if reg.Official("openai", "https://someblog.example/openai-news") {
		t.Error("a blog must not be official")
	}
	if reg.Official("openai", "https://evil-platform.openai.com.example/x") {
		t.Error("a look-alike host must not be official")
	}
	if !reg.Official("google", "https://ai.google.dev/gemini-api/docs/deprecations") {
		t.Error("aliases should resolve to the provider's entry")
	}
	if got := DomainOf("https://WWW.Example.com/a/b?c=d"); got != "example.com" {
		t.Errorf("DomainOf = %q", got)
	}
}
