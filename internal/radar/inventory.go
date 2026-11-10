// Package radar finds out which models the gateway depends on and what
// their providers have announced about them.
package radar

import (
	"context"
	"sort"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/bifrost"
	"github.com/angarkartanmay-ops/globalaihack/internal/core"
)

// Gateway is the part of the Bifrost client the radar uses.
type Gateway interface {
	Rankings(ctx context.Context, f bifrost.LogFilter) ([]bifrost.Ranking, error)
	RankingsBy(ctx context.Context, dimension string, f bifrost.LogFilter) ([]bifrost.DimensionRanking, error)
	Logs(ctx context.Context, f bifrost.LogFilter) (*bifrost.LogPage, error)
	ListModels(ctx context.Context) ([]bifrost.ModelInfo, error)
	RetrieveModel(ctx context.Context, id string) (*bifrost.ModelInfo, error)
}

// maxConsumerBreakdowns bounds the per-model consumer queries per snapshot.
const maxConsumerBreakdowns = 40

// BuildInventory asks the gateway which models carried traffic in the
// lookback window. The gateway does the aggregation; Heimdall never scans raw
// logs for this. Heimdall's own requests (tagged in log metadata) are
// subtracted so that rehearsals and analyst calls never look like demand.
func BuildInventory(ctx context.Context, gw Gateway, lookback time.Duration, now time.Time) (*core.Inventory, error) {
	start := now.Add(-lookback)
	window := bifrost.LogFilter{Start: start, End: now}

	all, err := gw.Rankings(ctx, window)
	if err != nil {
		return nil, err
	}
	own := map[string]bifrost.Ranking{}
	ownFilter := window
	ownFilter.Metadata = map[string]string{bifrost.TagKey: "1"}
	if rs, err := gw.Rankings(ctx, ownFilter); err == nil {
		for _, r := range rs {
			own[r.Provider+"/"+r.Model] = r
		}
	}

	inv := &core.Inventory{TakenAt: now, WindowStart: start, WindowEnd: now}
	for _, r := range all {
		o := own[r.Provider+"/"+r.Model]
		reqs := r.TotalRequests - o.TotalRequests
		if reqs <= 0 || r.Model == "" {
			continue
		}
		succ := r.SuccessCount - o.SuccessCount
		item := core.InventoryItem{
			ModelRef:     core.ModelRef{Provider: r.Provider, Model: r.Model},
			Requests:     reqs,
			Tokens:       max(0, r.TotalTokens-o.TotalTokens),
			Cost:         max(0, r.TotalCost-o.TotalCost),
			AvgLatencyMs: r.AvgLatency,
		}
		if succ > 0 {
			item.SuccessRate = 100 * float64(succ) / float64(reqs)
		}
		inv.Items = append(inv.Items, item)
		inv.Requests += reqs
		inv.Cost += item.Cost
	}

	// Daily rates use the span traffic was actually observed over, so a
	// gateway that has been up for two hours is not averaged over seven days.
	days := lookback.Hours() / 24
	if page, err := gw.Logs(ctx, bifrost.LogFilter{Start: start, End: now, Limit: 1, Order: "asc"}); err == nil && len(page.Logs) > 0 {
		if span := now.Sub(page.Logs[0].Timestamp).Hours() / 24; span > 0 && span < days {
			days = span
		}
	}
	if days < 1.0/24 {
		days = 1.0 / 24
	}
	inv.WindowDays = days

	sort.Slice(inv.Items, func(i, j int) bool {
		if inv.Items[i].Requests != inv.Items[j].Requests {
			return inv.Items[i].Requests > inv.Items[j].Requests
		}
		return inv.Items[i].ID() < inv.Items[j].ID()
	})
	for i := range inv.Items {
		it := &inv.Items[i]
		if inv.Requests > 0 {
			it.RequestShare = float64(it.Requests) / float64(inv.Requests)
		}
		if inv.Cost > 0 {
			it.CostShare = it.Cost / inv.Cost
		}
		it.DailyReqs = float64(it.Requests) / days
		it.DailyCost = it.Cost / days
		if i >= maxConsumerBreakdowns {
			continue
		}
		f := bifrost.LogFilter{Start: start, End: now, Providers: []string{it.Provider}, Models: []string{it.Model}}
		rows, err := gw.RankingsBy(ctx, "virtual_key", f)
		if err != nil {
			continue
		}
		ownRows := map[string]bifrost.DimensionRanking{}
		if _, has := own[it.ID()]; has {
			f.Metadata = map[string]string{bifrost.TagKey: "1"}
			if rs, err := gw.RankingsBy(ctx, "virtual_key", f); err == nil {
				for _, r := range rs {
					ownRows[r.ID] = r
				}
			}
		}
		for _, r := range rows {
			o := ownRows[r.ID]
			if n := r.TotalRequests - o.TotalRequests; n > 0 {
				name := r.Name
				if name == "" {
					name = r.ID
				}
				it.Consumers = append(it.Consumers, core.ConsumerUsage{ID: r.ID, Name: name, Requests: n, Cost: max(0, r.TotalCost-o.TotalCost)})
			}
		}
		sort.Slice(it.Consumers, func(a, b int) bool { return it.Consumers[a].Requests > it.Consumers[b].Requests })
	}
	return inv, nil
}

// Risk classifies an exposure from the finding and the days left.
func Risk(f *core.Finding, daysLeft *int, hasTraffic bool) string {
	if f == nil {
		return core.RiskNone
	}
	switch f.Status {
	case core.StatusRetired:
		if hasTraffic {
			return core.RiskCritical
		}
		return core.RiskLow
	case core.StatusDeprecated:
		if daysLeft == nil {
			return core.RiskMedium
		}
		switch d := *daysLeft; {
		case d <= 14:
			return core.RiskCritical
		case d <= 45:
			return core.RiskHigh
		case d <= 120:
			return core.RiskMedium
		default:
			return core.RiskLow
		}
	}
	return core.RiskNone
}

// RiskRank orders risks, most urgent first.
func RiskRank(r string) int {
	switch r {
	case core.RiskCritical:
		return 0
	case core.RiskHigh:
		return 1
	case core.RiskMedium:
		return 2
	case core.RiskLow:
		return 3
	}
	return 4
}
