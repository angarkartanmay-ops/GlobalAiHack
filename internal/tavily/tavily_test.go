package tavily

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request and response shapes here follow Tavily's published API as
// implemented by its official Python SDK (tavily-python): POST /search and
// POST /extract with a bearer key, and errors as {"detail": {"error": "..."}}.

func TestDirectSearchRequestShape(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"query":"q","answer":null,"results":[{"title":"T","url":"https://a.example/x","content":"snippet","raw_content":"full page","score":0.91,"published_date":"2026-10-01"}],"response_time":1.2}`))
	}))
	defer srv.Close()

	c := NewDirect("tvly-key", srv.URL)
	resp, err := c.Search(context.Background(), SearchRequest{Query: "q", SearchDepth: "advanced", MaxResults: 5,
		IncludeDomains: []string{"docs.example.com"}, IncludeRawContent: "markdown"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/search" || gotAuth != "Bearer tvly-key" {
		t.Errorf("path=%s auth=%s", gotPath, gotAuth)
	}
	if gotBody["query"] != "q" || gotBody["search_depth"] != "advanced" || gotBody["max_results"] != float64(5) || gotBody["include_raw_content"] != "markdown" {
		t.Errorf("body = %v", gotBody)
	}
	// unset options are left out so Tavily applies its own defaults
	for _, k := range []string{"topic", "time_range", "exclude_domains", "include_answer"} {
		if _, present := gotBody[k]; present {
			t.Errorf("unset option %q should be omitted", k)
		}
	}
	if len(resp.Results) != 1 || resp.Results[0].Text() != "full page" || resp.Results[0].PublishedDate != "2026-10-01" {
		t.Errorf("results = %+v", resp.Results)
	}
	if s, e := c.Counts(); s != 1 || e != 0 {
		t.Errorf("counts = %d %d", s, e)
	}
}

func TestDirectExtract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"url":"https://a.example/x","raw_content":"page"}],"failed_results":[{"url":"https://b.example/y","error":"timeout"}]}`))
	}))
	defer srv.Close()
	resp, err := NewDirect("k", srv.URL).Extract(context.Background(), ExtractRequest{URLs: []string{"https://a.example/x", "https://b.example/y"}, Format: "markdown"})
	if err != nil || len(resp.Results) != 1 || len(resp.FailedResults) != 1 || resp.FailedResults[0].Error != "timeout" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
}

func TestDirectErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`, "invalid or missing Tavily API key"},
		{429, `{"detail":{"error":"Your request has been blocked due to excessive requests."}}`, "rate limit"},
		{432, `{"detail":{"error":"This request exceeds your plan's set usage limit."}}`, "plan or credit limit"},
		{500, `upstream exploded`, "upstream exploded"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		_, err := NewDirect("k", srv.URL).Search(context.Background(), SearchRequest{Query: "q"})
		srv.Close()
		var te *Error
		if !errors.As(err, &te) || te.Status != c.status || !strings.Contains(err.Error(), c.want) {
			t.Errorf("status %d: err = %v", c.status, err)
		}
	}
}

type fakeRelay struct {
	relay, path string
	err         error
}

func (f *fakeRelay) Relay(_ context.Context, relay, path string, _, out any) error {
	f.relay, f.path = relay, path
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(`{"results":[{"url":"https://a.example","content":"c"}]}`), out)
}

func TestViaRelay(t *testing.T) {
	gw := &fakeRelay{}
	c := NewViaRelay(gw, "tavily", nil)
	resp, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	if err != nil || gw.relay != "tavily" || gw.path != "/search" || len(resp.Results) != 1 || c.Mode() != "relay:tavily" {
		t.Fatalf("relay=%s path=%s resp=%+v err=%v", gw.relay, gw.path, resp, err)
	}
	// a gateway refusal reads like a Tavily error, with a hint about the grant
	denied := NewViaRelay(&fakeRelay{err: errors.New("403")}, "tavily", func(error) (int, string, bool) { return 403, "Relay 'tavily' is not allowed", true })
	if _, err := denied.Extract(context.Background(), ExtractRequest{URLs: []string{"x"}}); err == nil || !strings.Contains(err.Error(), "relay grant") {
		t.Errorf("err = %v", err)
	}
}
