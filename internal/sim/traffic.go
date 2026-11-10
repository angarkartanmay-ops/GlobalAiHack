package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
)

// Sender sends an application request through the gateway.
type Sender interface {
	ChatAs(ctx context.Context, req bifrost.ChatRequest, virtualKey string, hdr map[string]string) (*bifrost.ChatResponse, error)
}

// Item is one application request.
type Item struct {
	Consumer string // virtual key value
	App      string
	Model    string // provider/model
	Messages []map[string]any
	Extra    map[string]any
}

// ToolsJSON is the tool list the simulated order assistant is given.
const ToolsJSON = `[
 {"type":"function","function":{"name":"lookup_order","description":"Look up an order by its id","parameters":{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}}},
 {"type":"function","function":{"name":"track_parcel","description":"Get the latest scan for a parcel","parameters":{"type":"object","properties":{"tracking_number":{"type":"string"}},"required":["tracking_number"]}}}
]`

// SupportQuestions are what customers ask the support assistant. Several
// touch facts that change over time.
var SupportQuestions = []string{
	"Which API version should I integrate with for a new project?",
	"What version of the Northpost API is current?",
	"What is the weekend delivery surcharge?",
	"Is there an extra fee for weekend delivery?",
	"Who is the CEO of Northpost?",
	"How long does standard delivery take?",
	"What is the standard delivery time for a parcel?",
	"How do I return a parcel?",
	"What is your returns policy?",
	"How can I track my parcel?",
	"Where can I see tracking updates for my parcel?",
	"Can I integrate the Northpost API into my shop, and which version?",
}

func msgs(system, user string) []map[string]any {
	return []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": user}}
}

// Workload returns the distinct requests the simulated applications send.
// It works against any chat model, so live mode reuses it to seed a fresh
// gateway with realistic traffic: pass the models to address.
func Workload(large, small, other, legacy string, consumers [4]string) []Item {
	var tools []any
	_ = json.Unmarshal([]byte(ToolsJSON), &tools)
	checkout, support, search, batch := consumers[0], consumers[1], consumers[2], consumers[3]
	var items []Item
	add := func(c, app, model string, m []map[string]any, extra map[string]any) {
		if model != "" {
			items = append(items, Item{Consumer: c, App: app, Model: model, Messages: m, Extra: extra})
		}
	}

	// checkout-app: structured extraction and tool calls on the large model.
	templates := []string{
		"Order %d arrived damaged, I need a replacement today.",
		"I want a refund for order %d.",
		"Where is order %d? It hasn't arrived.",
		"Please cancel order %d immediately.",
		"Question about order %d: can I change the delivery address?",
	}
	for i := 0; i < 40; i++ {
		id := 40000 + i*731%50000
		add(checkout, "checkout-app", large, msgs(SysExtract, fmt.Sprintf(templates[i%len(templates)], id)),
			map[string]any{"response_format": map[string]string{"type": "json_object"}, "temperature": 0})
	}
	for i := 0; i < 24; i++ {
		id := 51000 + i*613%40000
		user := fmt.Sprintf("Where is order %d?", id)
		if i%3 == 1 {
			user = fmt.Sprintf("Can you check the status of order %d for me?", id)
		}
		if i%6 == 5 {
			user = fmt.Sprintf("Track parcel NP%08d please.", 10000000+i*7919)
		}
		add(checkout, "checkout-app", large, msgs(SysTools, user), map[string]any{"tools": tools, "tool_choice": "auto"})
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprint(62000 + i*211)
		m := msgs(SysTools, "Where is order "+id+"?")
		m = append(m,
			map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{"id": "call_" + id, "type": "function",
				"function": map[string]string{"name": "lookup_order", "arguments": fmt.Sprintf(`{"order_id": %q}`, id)}}}},
			map[string]any{"role": "tool", "tool_call_id": "call_" + id, "content": `{"status": "out for delivery"}`})
		add(checkout, "checkout-app", large, m, map[string]any{"tools": tools})
	}

	// support-bot: free-text answers, mostly on the large model.
	for i, q := range SupportQuestions {
		add(support, "support-bot", large, msgs(SysSupport, q), map[string]any{"temperature": 0.2, "max_tokens": 300})
		if i%2 == 0 {
			add(support, "support-bot", other, msgs(SysSupport, q), map[string]any{"temperature": 0.2})
		}
	}

	// search-service: high-volume query rewriting on the small model.
	cities := []string{"berlin", "madrid", "oslo", "lisbon", "vienna", "dublin", "prague", "helsinki"}
	for i, c := range cities {
		add(search, "search-service", small, msgs(SysRewrite, fmt.Sprintf("cheap shipping to %s under %dkg", c, 2+i%4)), map[string]any{"temperature": 0})
		add(search, "search-service", small, msgs(SysRewrite, "express parcel to "+c), map[string]any{"temperature": 0})
	}

	// legacy-batch: a forgotten job still pointed at an old model.
	for i := 0; i < 4; i++ {
		add(batch, "legacy-batch", legacy, msgs(SysSupport, fmt.Sprintf("Summarise the failed deliveries for depot %d.", i+1)), nil)
	}
	return items
}

// DefaultWorkload is the workload against the simulated models.
func DefaultWorkload() []Item {
	c := [4]string{Consumers[0].Value, Consumers[1].Value, Consumers[2].Value, Consumers[3].Value}
	items := Workload("aurora/aurora-large-1", "aurora/aurora-mini-1", "lumen/lumen-chat-3", "aurora/aurora-instant-0", c)
	for i, q := range SupportQuestions {
		if i%4 == 0 {
			items = append(items, Item{Consumer: c[3], App: "legacy-batch", Model: "lumen/lumen-chat-2-0815", Messages: msgs(SysSupport, q)})
		}
	}
	return items
}

func (it Item) request() bifrost.ChatRequest {
	raw, _ := json.Marshal(it.Messages)
	extra := map[string]json.RawMessage{}
	for k, v := range it.Extra {
		b, _ := json.Marshal(v)
		extra[k] = b
	}
	return bifrost.ChatRequest{Model: it.Model, Messages: raw, Extra: extra}
}

// Stats counts what a traffic run sent.
type Stats struct {
	Sent   atomic.Int64
	Failed atomic.Int64
}

// Send sends each item once, with bounded concurrency.
func Send(ctx context.Context, s Sender, items []Item, concurrency int, st *Stats) {
	if concurrency <= 0 {
		concurrency = 6
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it Item) {
			defer wg.Done()
			defer func() { <-sem }()
			_, err := s.ChatAs(ctx, it.request(), it.Consumer, map[string]string{"User-Agent": "northpost-" + it.App + "/1.0"})
			st.Sent.Add(1)
			if err != nil {
				st.Failed.Add(1)
			}
		}(it)
	}
	wg.Wait()
}

// Trickle keeps a low, steady rate of application traffic flowing so that
// canary splits and failovers have live requests to act on. It stops when
// ctx is cancelled or after limit requests.
func Trickle(ctx context.Context, s Sender, items []Item, interval time.Duration, limit int64, st *Stats) {
	if len(items) == 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if st.Sent.Load() >= limit {
				return
			}
			it := items[rand.Intn(len(items))]
			go func() {
				cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				_, err := s.ChatAs(cctx, it.request(), it.Consumer, map[string]string{"User-Agent": "northpost-" + it.App + "/1.0"})
				st.Sent.Add(1)
				if err != nil {
					st.Failed.Add(1)
				}
			}()
		}
	}
}
