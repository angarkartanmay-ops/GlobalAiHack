// Package analyst runs Heimdall's own model requests (extracting notices,
// judging replays, assessing claims) through the Bifrost gateway.
//
// Every request goes out under Heimdall's virtual key with the gateway's
// request-level fallbacks, so the analyst inherits whatever resilience,
// budgets and logging the operator has configured. Heimdall never talks to a
// model provider directly.
package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
)

// LLM is what the radar, the rehearsal judge and the freshness checker use.
type LLM interface {
	// JSON asks for a single JSON object and decodes it into out.
	JSON(ctx context.Context, task, system, user string, out any, run string) error
	// Text asks for plain prose.
	Text(ctx context.Context, task, system, user string, run string) (string, error)
	Calls() int64
	ModelName() string
}

// Chatter is the part of the Bifrost client the analyst needs.
type Chatter interface {
	Chat(ctx context.Context, req bifrost.ChatRequest, tags bifrost.Tags) (*bifrost.ChatResponse, error)
}

// Analyst is an LLM backed by a model served through Bifrost.
type Analyst struct {
	GW          Chatter
	Model       string   // provider/model
	Fallbacks   []string // provider/model, tried by the gateway in order
	MaxTokens   int
	Kind        string // log tag: radar, judge, freshness
	calls       atomic.Int64
	noTemp      atomic.Bool
	noMaxTokens atomic.Bool
}

// New returns an analyst for the given model.
func New(gw Chatter, model string, fallbacks []string, kind string) *Analyst {
	return &Analyst{GW: gw, Model: model, Fallbacks: fallbacks, MaxTokens: 1500, Kind: kind}
}

func (a *Analyst) Calls() int64      { return a.calls.Load() }
func (a *Analyst) ModelName() string { return a.Model }

// TaskMarker labels a system prompt with the task it belongs to. It makes
// Heimdall's requests easy to find in gateway logs, and it is how the bundled
// simulator recognises which task it is standing in for.
func TaskMarker(task string) string { return "[heimdall-task: " + task + "]" }

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (a *Analyst) complete(ctx context.Context, task, system, user, run string, msgs []chatMsg) (string, error) {
	if a.Model == "" {
		return "", errors.New("no analyst model configured (set HEIMDALL_ANALYST_MODEL)")
	}
	if msgs == nil {
		msgs = []chatMsg{{"system", TaskMarker(task) + "\n" + system}, {"user", user}}
	}
	raw, _ := json.Marshal(msgs)
	for attempt := 0; attempt < 3; attempt++ {
		extra := map[string]json.RawMessage{}
		if !a.noTemp.Load() {
			extra["temperature"] = json.RawMessage("0")
		}
		if !a.noMaxTokens.Load() && a.MaxTokens > 0 {
			extra["max_tokens"] = json.RawMessage(fmt.Sprint(a.MaxTokens))
		}
		a.calls.Add(1)
		resp, err := a.GW.Chat(ctx, bifrost.ChatRequest{Model: a.Model, Messages: raw, Fallbacks: a.Fallbacks, Extra: extra},
			bifrost.Tags{Kind: firstNonEmpty(a.Kind, task), Run: run})
		if err != nil {
			// Some models reject sampling or length parameters. Drop the one
			// the provider names and try again rather than failing the task.
			if bifrost.IsStatus(err, 400) {
				low := strings.ToLower(err.Error())
				if strings.Contains(low, "temperature") && !a.noTemp.Load() {
					a.noTemp.Store(true)
					continue
				}
				if strings.Contains(low, "max_tokens") && !a.noMaxTokens.Load() {
					a.noMaxTokens.Store(true)
					continue
				}
			}
			return "", fmt.Errorf("analyst %s: %w", task, err)
		}
		msg, _ := resp.First()
		return msg.Text(), nil
	}
	return "", fmt.Errorf("analyst %s: model rejected the request parameters", task)
}

// Text asks the analyst model for prose.
func (a *Analyst) Text(ctx context.Context, task, system, user, run string) (string, error) {
	return a.complete(ctx, task, system, user, run, nil)
}

// JSON asks the analyst model for one JSON object. A reply that does not
// parse gets one repair round, with the parse error shown to the model.
func (a *Analyst) JSON(ctx context.Context, task, system, user string, out any, run string) error {
	system += "\n\nReply with a single JSON object and nothing else: no prose, no markdown fences."
	text, err := a.complete(ctx, task, system, user, run, nil)
	if err != nil {
		return err
	}
	perr := DecodeJSON(text, out)
	if perr == nil {
		return nil
	}
	msgs := []chatMsg{
		{"system", TaskMarker(task) + "\n" + system},
		{"user", user},
		{"assistant", text},
		{"user", "That reply was not valid JSON (" + perr.Error() + "). Send the same answer again as one valid JSON object, with nothing before or after it."},
	}
	text, err = a.complete(ctx, task, system, user, run, msgs)
	if err != nil {
		return err
	}
	if perr = DecodeJSON(text, out); perr != nil {
		return fmt.Errorf("analyst %s: reply is not valid JSON after repair: %w", task, perr)
	}
	return nil
}

// DecodeJSON extracts the first JSON object from a model reply, tolerating
// markdown fences and surrounding prose, and decodes it into out.
func DecodeJSON(text string, out any) error {
	obj, err := ExtractObject(text)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(obj), out)
}

// ExtractObject returns the first balanced {...} in text.
func ExtractObject(text string) (string, error) {
	start := strings.Index(text, "{")
	if start < 0 {
		return "", errors.New("no JSON object in reply")
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], nil
			}
		}
	}
	return "", errors.New("unterminated JSON object in reply")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Str decodes a JSON string, number, boolean or null into a Go string. Model
// replies are not reliable about which of those they use for an optional field.
type Str string

// UnmarshalJSON implements json.Unmarshaler.
func (s *Str) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	if t == "null" || t == "" {
		*s = ""
		return nil
	}
	var v string
	if err := json.Unmarshal(b, &v); err == nil {
		v = strings.TrimSpace(v)
		switch strings.ToLower(v) {
		case "null", "none", "n/a", "unknown":
			v = ""
		}
		*s = Str(v)
		return nil
	}
	*s = Str(strings.Trim(t, `"`))
	return nil
}

// Bool decodes true/false, "true"/"yes" and 1/0.
type Bool bool

// UnmarshalJSON implements json.Unmarshaler.
func (b *Bool) UnmarshalJSON(data []byte) error {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(string(data)), `"`))
	*b = t == "true" || t == "yes" || t == "1"
	return nil
}
