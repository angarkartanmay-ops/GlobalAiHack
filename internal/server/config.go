// Package server wires Heimdall together: configuration, the scan
// scheduler, the HTTP API and the embedded dashboard.
package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
)

// Modes.
const (
	ModeSim  = "sim"  // real gateway, simulated providers and web; no keys needed
	ModeLive = "live" // real gateway, real providers, real Tavily
)

// Config is Heimdall's runtime configuration, read from the environment.
type Config struct {
	Addr    string
	DataDir string
	Mode    string

	// Gateway.
	BifrostURL string
	AdminUser  string
	AdminPass  string
	VirtualKey string

	// Models used for Heimdall's own reasoning, as provider/model. When
	// AnalystModel is empty in live mode, Heimdall picks a small, current
	// model from the gateway's model list instead of hardcoding one that may
	// itself be retired.
	AnalystModel     string
	AnalystFallbacks []string
	JudgeModel       string

	// Tavily. With TavilyRelay set, calls go through that Bifrost relay and
	// Heimdall holds no Tavily key. Otherwise TavilyAPIKey is used directly.
	TavilyRelay   string
	TavilyAPIKey  string
	TavilyBaseURL string
	OpenWeb       bool // also search outside the provider's own domains

	Lookback     time.Duration
	ScanInterval time.Duration
	SourcesFile  string

	RehearsalSamples int
	RehearsalBudget  float64
	AutoRollback     bool

	AlertWebhook   string
	AdminToken     string
	AllowAnonymous bool

	// Simulator.
	SimKey     string
	SimTraffic bool

	// Virtual keys the sample-traffic sender uses in live mode
	// (checkout-app, support-bot, search-service, legacy-batch).
	TrafficKeys [4]string
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if strings.HasSuffix(v, "d") {
		if n, err := strconv.ParseFloat(strings.TrimSuffix(v, "d"), 64); err == nil {
			return time.Duration(n * 24 * float64(time.Hour))
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(key)), 64); err == nil {
		return f
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return n
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadConfig reads configuration from the environment.
func LoadConfig() Config {
	port := env("PORT", "8787")
	c := Config{
		Addr:             env("HEIMDALL_ADDR", ":"+port),
		DataDir:          env("HEIMDALL_DATA_DIR", "./data"),
		Mode:             strings.ToLower(env("HEIMDALL_MODE", "")),
		BifrostURL:       env("BIFROST_URL", "http://127.0.0.1:8080"),
		AdminUser:        env("BIFROST_ADMIN_USERNAME", ""),
		AdminPass:        env("BIFROST_ADMIN_PASSWORD", ""),
		VirtualKey:       env("BIFROST_VIRTUAL_KEY", ""),
		AnalystModel:     env("HEIMDALL_ANALYST_MODEL", ""),
		AnalystFallbacks: splitList(os.Getenv("HEIMDALL_ANALYST_FALLBACKS")),
		JudgeModel:       env("HEIMDALL_JUDGE_MODEL", ""),
		TavilyRelay:      env("HEIMDALL_TAVILY_RELAY", ""),
		TavilyAPIKey:     env("TAVILY_API_KEY", ""),
		TavilyBaseURL:    env("TAVILY_BASE_URL", ""),
		OpenWeb:          envBool("HEIMDALL_OPEN_WEB", true),
		Lookback:         envDuration("HEIMDALL_LOOKBACK", 7*24*time.Hour),
		ScanInterval:     envDuration("HEIMDALL_SCAN_INTERVAL", 12*time.Hour),
		SourcesFile:      env("HEIMDALL_SOURCES_FILE", ""),
		RehearsalSamples: envInt("HEIMDALL_REHEARSAL_SAMPLES", 25),
		RehearsalBudget:  envFloat("HEIMDALL_REHEARSAL_BUDGET_USD", 1.0),
		AutoRollback:     envBool("HEIMDALL_CANARY_AUTO_ROLLBACK", false),
		AlertWebhook:     env("HEIMDALL_ALERT_WEBHOOK_URL", ""),
		AdminToken:       env("HEIMDALL_ADMIN_TOKEN", ""),
		AllowAnonymous:   envBool("HEIMDALL_ALLOW_ANONYMOUS", false),
		SimKey:           env("HEIMDALL_SIM_KEY", sim.DefaultKey),
		SimTraffic:       envBool("HEIMDALL_SIM_TRAFFIC", true),
	}
	// With no relay named and a Tavily key in hand, call Tavily directly.
	// Otherwise assume the conventional relay name.
	if c.TavilyRelay == "" && c.TavilyAPIKey == "" {
		c.TavilyRelay = "tavily"
	}
	if c.Mode != ModeSim && c.Mode != ModeLive {
		// Pointing Heimdall at a gateway with a virtual key means live.
		if os.Getenv("BIFROST_VIRTUAL_KEY") != "" {
			c.Mode = ModeLive
		} else {
			c.Mode = ModeSim
		}
	}
	keys := splitList(os.Getenv("HEIMDALL_TRAFFIC_KEYS"))
	for i := 0; i < 4 && i < len(keys); i++ {
		c.TrafficKeys[i] = keys[i]
	}
	if c.Mode == ModeSim {
		if c.VirtualKey == "" {
			c.VirtualKey = sim.HeimdallKey
		}
		if c.AnalystModel == "" {
			c.AnalystModel = sim.AnalystModel
		}
		for i, cons := range sim.Consumers {
			c.TrafficKeys[i] = cons.Value
		}
	}
	if c.JudgeModel == "" {
		c.JudgeModel = c.AnalystModel
	}
	return c
}

// Validate reports configuration problems that would stop Heimdall working.
func (c Config) Validate() []string {
	var problems []string
	if c.Mode == ModeLive {
		if c.VirtualKey == "" {
			problems = append(problems, "BIFROST_VIRTUAL_KEY is not set: Heimdall needs a Bifrost virtual key for its model and Tavily calls.")
		}
		if c.AdminUser == "" || c.AdminPass == "" {
			problems = append(problems, "BIFROST_ADMIN_USERNAME / BIFROST_ADMIN_PASSWORD are not set: Heimdall reads logs and writes routing rules through the gateway's admin API.")
		}
		if c.TavilyRelay == "" && c.TavilyAPIKey == "" {
			problems = append(problems, "No Tavily access: set HEIMDALL_TAVILY_RELAY to a Bifrost relay name, or TAVILY_API_KEY for direct calls.")
		}
		if c.AdminToken == "" && !c.AllowAnonymous {
			problems = append(problems, "HEIMDALL_ADMIN_TOKEN is not set. In live mode the dashboard shows real request content and can change gateway routing, so it stays locked until a token is set (or HEIMDALL_ALLOW_ANONYMOUS=true for local use).")
		}
	}
	return problems
}

// Describe returns a one-line summary for the startup log, without secrets.
func (c Config) Describe() string {
	tav := "direct"
	if c.TavilyRelay != "" {
		tav = "relay:" + c.TavilyRelay
	}
	return fmt.Sprintf("mode=%s gateway=%s tavily=%s analyst=%s lookback=%s scan_every=%s", c.Mode, c.BifrostURL, tav, orAuto(c.AnalystModel), c.Lookback, c.ScanInterval)
}

func orAuto(s string) string {
	if s == "" {
		return "(auto)"
	}
	return s
}
