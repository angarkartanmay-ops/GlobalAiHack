package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
)

func TestDecodeJSONTolerance(t *testing.T) {
	var out struct {
		A Str  `json:"a"`
		B Bool `json:"b"`
	}
	cases := []string{
		`{"a": "x", "b": true}`,
		"```json\n{\"a\": \"x\", \"b\": \"yes\"}\n```",
		`Here you go: {"a": "x", "b": 1} Hope that helps.`,
		`{"a": "x", "b": true, "nested": {"q": "a } brace in a string"}}`,
	}
	for _, c := range cases {
		out.A, out.B = "", false
		if err := DecodeJSON(c, &out); err != nil || out.A != "x" || !bool(out.B) {
			t.Errorf("DecodeJSON(%q) = %+v, %v", c, out, err)
		}
	}
	if err := DecodeJSON("no json here", &out); err == nil {
		t.Error("expected an error when there is no object")
	}
	if err := DecodeJSON(`{"a": "unterminated`, &out); err == nil {
		t.Error("expected an error for an unterminated object")
	}
	var s struct {
		V Str `json:"v"`
	}
	for _, c := range []string{`{"v": null}`, `{"v": "null"}`, `{"v": "N/A"}`} {
		s.V = "keep"
		if err := DecodeJSON(c, &s); err != nil || s.V != "" {
			t.Errorf("%s should decode to empty, got %q %v", c, s.V, err)
		}
	}
}

type fakeGW struct {
	replies []string
	errs    []error
	seen    []bifrost.ChatRequest
}

func (f *fakeGW) Chat(_ context.Context, req bifrost.ChatRequest, _ bifrost.Tags) (*bifrost.ChatResponse, error) {
	i := len(f.seen)
	f.seen = append(f.seen, req)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	var resp bifrost.ChatResponse
	body := `{"choices":[{"message":{"role":"assistant","content":` + string(mustJSON(f.replies[i])) + `}}]}`
	_ = json.Unmarshal([]byte(body), &resp)
	return &resp, nil
}

func mustJSON(s string) []byte { b, _ := json.Marshal(s); return b }

func TestJSONRepairsOnce(t *testing.T) {
	gw := &fakeGW{replies: []string{"Sure! The answer is forty-two.", `{"n": "42"}`}}
	a := New(gw, "p/m", []string{"p/backup"}, "radar")
	var out struct {
		N Str `json:"n"`
	}
	if err := a.JSON(context.Background(), "t", "sys", "user", &out, "run"); err != nil || out.N != "42" {
		t.Fatalf("JSON = %+v, %v", out, err)
	}
	if len(gw.seen) != 2 || a.Calls() != 2 {
		t.Errorf("expected one repair round, saw %d calls", len(gw.seen))
	}
	body, _ := json.Marshal(gw.seen[0])
	if !strings.Contains(string(body), `"fallbacks":["p/backup"]`) || !strings.Contains(string(body), "[heimdall-task: t]") {
		t.Errorf("request should carry gateway fallbacks and the task marker: %s", body)
	}
}

func TestDropsRejectedParameters(t *testing.T) {
	gw := &fakeGW{
		errs:    []error{&bifrost.APIError{Status: 400, Message: "Unsupported parameter: 'temperature' is not supported with this model."}, nil},
		replies: []string{"", "plain text"},
	}
	a := New(gw, "p/m", nil, "radar")
	got, err := a.Text(context.Background(), "t", "sys", "user", "run")
	if err != nil || got != "plain text" {
		t.Fatalf("Text = %q, %v", got, err)
	}
	second, _ := json.Marshal(gw.seen[1])
	if strings.Contains(string(second), "temperature") {
		t.Errorf("retry should drop the rejected parameter: %s", second)
	}
	if _, err := New(&fakeGW{errs: []error{errors.New("down")}}, "p/m", nil, "x").Text(context.Background(), "t", "s", "u", "r"); err == nil {
		t.Error("gateway errors should surface")
	}
}
