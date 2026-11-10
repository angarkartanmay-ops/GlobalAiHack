package sim

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/angarkartanmay-ops/globalaihack/internal/evidence"
)

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the of to in on at for from by with and or is are was were be been it its this that these those as
		what which who whom when where why how do does did can could should would will shall may might must i you we they he she
		me my our your their there here not no yes about into over under after before than then so if but also any all each more most
		model models date shut shutdown down retirement retire retired deprecation deprecated current currently latest now today
		use used using new old per`) {
		stopwords[w] = true
	}
}

// keywords returns the content-bearing words of text, in canonical form.
func keywords(text string) []string {
	var out []string
	for _, t := range strings.Fields(evidence.Canon(text)) {
		if len(t) > 1 && !stopwords[t] {
			out = append(out, t)
		}
	}
	return out
}

func keywordSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, k := range keywords(text) {
		set[k] = true
	}
	return set
}

func overlap(a map[string]bool, text string) int {
	n := 0
	seen := map[string]bool{}
	for _, k := range keywords(text) {
		if a[k] && !seen[k] {
			seen[k] = true
			n++
		}
	}
	return n
}

var sentenceEnd = regexp.MustCompile(`([.!?])\s+`)

// sentences splits prose into sentences and keeps table rows and headings
// as units of their own.
func sentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "|") || strings.HasPrefix(line, "#") {
			out = append(out, line)
			continue
		}
		for _, s := range strings.Split(sentenceEnd.ReplaceAllString(line, "$1\n"), "\n") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// snippet returns the sentences of a page that best match the query, the way
// a search API returns the relevant part of a page rather than its opening.
func snippet(text string, q map[string]bool, n int) string {
	type scored struct {
		i, score int
		s        string
	}
	var ss []scored
	for i, s := range sentences(text) {
		if strings.HasPrefix(s, "|---") {
			continue
		}
		ss = append(ss, scored{i, overlap(q, s), s})
	}
	sort.SliceStable(ss, func(a, b int) bool { return ss[a].score > ss[b].score })
	if len(ss) > n {
		ss = ss[:n]
	}
	sort.Slice(ss, func(a, b int) bool { return ss[a].i < ss[b].i })
	parts := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.score > 0 {
			parts = append(parts, s.s)
		}
	}
	return strings.Join(parts, "\n")
}

func domainMatches(domain string, list []string) bool {
	for _, d := range list {
		d = strings.ToLower(strings.TrimSpace(d))
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

type searchReq struct {
	Query             string   `json:"query"`
	MaxResults        int      `json:"max_results"`
	IncludeDomains    []string `json:"include_domains"`
	ExcludeDomains    []string `json:"exclude_domains"`
	IncludeRawContent any      `json:"include_raw_content"`
}

func (w *World) handleSearch(rw http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"detail": map[string]string{"error": "query is required"}})
		return
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 5
	}
	wantRaw := req.IncludeRawContent != nil && req.IncludeRawContent != false
	q := keywordSet(req.Query)
	type hit struct {
		p     Page
		score float64
	}
	var hits []hit
	for _, p := range w.pages {
		d := evidence.DomainOf(p.URL)
		if len(req.IncludeDomains) > 0 && !domainMatches(d, req.IncludeDomains) {
			continue
		}
		if domainMatches(d, req.ExcludeDomains) {
			continue
		}
		n := overlap(q, p.Text) + 2*overlap(q, p.Title)
		if n < 2 {
			continue
		}
		hits = append(hits, hit{p, float64(n) / float64(len(q)*3)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > req.MaxResults {
		hits = hits[:req.MaxResults]
	}
	results := []map[string]any{}
	for _, h := range hits {
		score := h.score
		if score > 0.99 {
			score = 0.99
		}
		res := map[string]any{"title": h.p.Title, "url": h.p.URL, "content": snippet(h.p.Text, q, 3), "score": score, "published_date": h.p.Published}
		if wantRaw {
			res["raw_content"] = h.p.Text
		}
		results = append(results, res)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"query": req.Query, "results": results, "response_time": 0.12})
}

func (w *World) handleExtract(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs any `json:"urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"detail": map[string]string{"error": "invalid body"}})
		return
	}
	var urls []string
	switch u := req.URLs.(type) {
	case string:
		urls = []string{u}
	case []any:
		for _, x := range u {
			if s, ok := x.(string); ok {
				urls = append(urls, s)
			}
		}
	}
	results, failed := []map[string]any{}, []map[string]any{}
	for _, u := range urls {
		found := false
		for _, p := range w.pages {
			if p.URL == u {
				results = append(results, map[string]any{"url": u, "raw_content": p.Text})
				found = true
			}
		}
		if !found {
			failed = append(failed, map[string]any{"url": u, "error": "page not found on the simulated web"})
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"results": results, "failed_results": failed, "response_time": 0.2})
}

func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}
