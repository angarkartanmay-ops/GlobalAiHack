package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/angarkartanmay-ops/globalaihack/internal/sim"
)

// ProviderKeys maps the environment variable that holds a provider's API
// key to the Bifrost provider name it configures.
var ProviderKeys = []struct{ Env, Provider string }{
	{"OPENAI_API_KEY", "openai"},
	{"ANTHROPIC_API_KEY", "anthropic"},
	{"GEMINI_API_KEY", "gemini"},
	{"GROQ_API_KEY", "groq"},
	{"MISTRAL_API_KEY", "mistral"},
	{"OPENROUTER_API_KEY", "openrouter"},
	{"CEREBRAS_API_KEY", "cerebras"},
	{"XAI_API_KEY", "xai"},
	{"DEEPSEEK_API_KEY", "deepseek"},
	{"COHERE_API_KEY", "cohere"},
}

// BootstrapOptions controls gateway configuration generation.
type BootstrapOptions struct {
	Dir         string // gateway app dir; config.json is written here
	HeimdallURL string // how the gateway reaches Heimdall's simulator endpoints
	GatewayURL  string // how Heimdall reaches the gateway
	Mode        string // "", "sim" or "live"; empty picks live when a provider key is present
	BudgetUSD   float64
	NoMCP       bool // do not register Heimdall's MCP server on the gateway
	Getenv      func(string) string
}

// BootstrapResult describes what was generated.
type BootstrapResult struct {
	Mode      string
	Providers []string
	Tavily    bool
	Env       map[string]string // variables both processes need
	Notes     []string
}

type secrets struct {
	AdminUser   string    `json:"admin_user"`
	AdminPass   string    `json:"admin_pass"`
	SetupToken  string    `json:"setup_token"`
	HeimdallVK  string    `json:"heimdall_vk"`
	TrafficVKs  [4]string `json:"traffic_vks"`
	AdminToken  string    `json:"admin_token"`
	GeneratedBy string    `json:"generated_by"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// loadSecrets returns the generated secrets for this install, creating them
// on first run. They live next to the gateway's data so that a restart keeps
// the same keys and the gateway's stored configuration stays valid.
func loadSecrets(dir string) (secrets, error) {
	path := filepath.Join(dir, "heimdall-secrets.json")
	var s secrets
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &s) == nil && s.HeimdallVK != "" {
		return s, nil
	}
	s = secrets{AdminUser: "heimdall", AdminPass: randomHex(18), SetupToken: randomHex(24),
		HeimdallVK: "sk-bf-" + randomHex(24), AdminToken: randomHex(16), GeneratedBy: "heimdall bootstrap"}
	for i := range s.TrafficVKs {
		s.TrafficVKs[i] = "sk-bf-" + randomHex(24)
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return s, err
	}
	return s, nil
}

// Bootstrap writes a Bifrost config.json for either mode and returns the
// environment both the gateway and Heimdall should run with. Secrets are
// passed by environment reference ("env.NAME"), never written into the file.
func Bootstrap(opt BootstrapOptions) (BootstrapResult, error) {
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.Dir == "" {
		return BootstrapResult{}, errors.New("bootstrap: no gateway directory given")
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return BootstrapResult{}, err
	}
	abs, err := filepath.Abs(opt.Dir)
	if err != nil {
		return BootstrapResult{}, err
	}
	if opt.HeimdallURL == "" {
		opt.HeimdallURL = "http://127.0.0.1:8787"
	}
	if opt.GatewayURL == "" {
		opt.GatewayURL = "http://127.0.0.1:8080"
	}
	if opt.BudgetUSD <= 0 {
		opt.BudgetUSD = 5
	}
	sec, err := loadSecrets(abs)
	if err != nil {
		return BootstrapResult{}, err
	}

	res := BootstrapResult{Env: map[string]string{}}
	providers := map[string]any{}
	for _, pk := range ProviderKeys {
		if strings.TrimSpace(opt.Getenv(pk.Env)) == "" {
			continue
		}
		providers[pk.Provider] = map[string]any{
			"keys": []map[string]any{{"name": pk.Provider + "-key", "value": "env." + pk.Env, "models": []string{"*"}, "weight": 1.0}},
		}
		res.Providers = append(res.Providers, pk.Provider)
	}
	sort.Strings(res.Providers)

	res.Mode = strings.ToLower(opt.Mode)
	switch res.Mode {
	case ModeLive:
		if len(providers) == 0 {
			return res, errors.New("live mode needs at least one provider key (for example OPENAI_API_KEY, ANTHROPIC_API_KEY or GEMINI_API_KEY)")
		}
	case ModeSim:
	default:
		res.Mode = ModeSim
		if len(providers) > 0 {
			res.Mode = ModeLive
		}
	}

	adminUser := firstNonEmpty(opt.Getenv("BIFROST_ADMIN_USERNAME"), sec.AdminUser)
	adminPass := firstNonEmpty(opt.Getenv("BIFROST_ADMIN_PASSWORD"), sec.AdminPass)
	heimdallVK := firstNonEmpty(opt.Getenv("BIFROST_VIRTUAL_KEY"), sec.HeimdallVK)
	traffic := sec.TrafficVKs
	names := []string{"checkout-app", "support-bot", "search-service", "legacy-batch"}
	ids := []string{"vk-checkout", "vk-support", "vk-search", "vk-legacy"}

	cfg := map[string]any{
		"$schema":     "https://www.getbifrost.ai/schema",
		"version":     2,
		"setup_token": "env.BIFROST_SETUP_TOKEN",
		"client":      map[string]any{"enable_logging": true, "enforce_auth_on_inference": true},
	}
	relay := map[string]any{
		"name": "tavily", "description": "Tavily search and extract, used by Heimdall to read provider lifecycle notices",
		"auth":   map[string]string{"type": "bearer"},
		"routes": []map[string]string{{"method": "POST", "path": "/search"}, {"method": "POST", "path": "/extract"}},
	}

	if res.Mode == ModeSim {
		heimdallVK = sim.HeimdallKey
		for i, c := range sim.Consumers {
			traffic[i], names[i], ids[i] = c.Value, c.Name, c.ID
		}
		providers = map[string]any{}
		for _, p := range []string{sim.ProviderAurora, sim.ProviderLumen} {
			providers[p] = map[string]any{
				"custom_provider_config": map[string]string{"base_provider_type": "openai"},
				"network_config":         map[string]any{"base_url": strings.TrimRight(opt.HeimdallURL, "/") + "/sim/" + p, "default_request_timeout_in_seconds": 60},
				"keys":                   []map[string]any{{"name": p + "-sim-key", "value": "env.HEIMDALL_SIM_KEY", "models": []string{"*"}, "weight": 1.0}},
			}
		}
		res.Providers = []string{sim.ProviderAurora, sim.ProviderLumen}
		relay["base_url"] = strings.TrimRight(opt.HeimdallURL, "/") + "/sim/tavily"
		relay["credential"] = map[string]string{"value": "env.HEIMDALL_SIM_KEY"}
		res.Tavily = true

		// The simulated models are not in Bifrost's public price sheet, so
		// the gateway reads their prices from a local file.
		pricing, _ := json.MarshalIndent(sim.Pricing(), "", "  ")
		if err := os.WriteFile(filepath.Join(abs, "pricing.json"), pricing, 0o644); err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(abs, "model-parameters.json"), []byte("{}\n"), 0o644); err != nil {
			return res, err
		}
		cfg["framework"] = map[string]any{"pricing": map[string]any{
			"pricing_url":           "file://" + filepath.Join(abs, "pricing.json"),
			"model_parameters_url":  "file://" + filepath.Join(abs, "model-parameters.json"),
			"pricing_sync_interval": 86400,
		}}
		res.Env["HEIMDALL_SIM_KEY"] = firstNonEmpty(opt.Getenv("HEIMDALL_SIM_KEY"), sim.DefaultKey)
		res.Notes = append(res.Notes, "No provider key found: running against the built-in simulator. Add a provider key and TAVILY_API_KEY to go live.")
	} else {
		relay["base_url"] = firstNonEmpty(opt.Getenv("TAVILY_BASE_URL"), "https://api.tavily.com")
		relay["credential"] = map[string]string{"value": "env.TAVILY_API_KEY"}
		res.Tavily = strings.TrimSpace(opt.Getenv("TAVILY_API_KEY")) != ""
		if !res.Tavily {
			res.Notes = append(res.Notes, "TAVILY_API_KEY is not set: the radar can only use lifecycle dates that provider APIs report. Add the key to read provider documentation.")
		}
	}

	// Air-gapped installs can point the gateway at their own price sheet.
	if u := strings.TrimSpace(opt.Getenv("BIFROST_PRICING_URL")); u != "" && res.Mode == ModeLive {
		pricing := map[string]any{"pricing_url": u, "pricing_sync_interval": 86400}
		if mp := strings.TrimSpace(opt.Getenv("BIFROST_MODEL_PARAMETERS_URL")); mp != "" {
			pricing["model_parameters_url"] = mp
		}
		cfg["framework"] = map[string]any{"pricing": pricing}
	}

	vks := []map[string]any{{
		"id": "vk-heimdall", "name": "heimdall", "value": "env.HEIMDALL_VK", "is_active": true, "allow_all_providers": true,
	}}
	if res.Tavily {
		cfg["relays"] = []any{relay}
		vks[0]["relay_grants"] = []map[string]string{{"relay_name": "tavily"}}
	}
	for i := range traffic {
		vks = append(vks, map[string]any{"id": ids[i], "name": names[i], "value": fmt.Sprintf("env.HEIMDALL_TRAFFIC_VK_%d", i+1), "is_active": true, "allow_all_providers": true})
		res.Env[fmt.Sprintf("HEIMDALL_TRAFFIC_VK_%d", i+1)] = traffic[i]
	}
	cfg["providers"] = providers
	cfg["governance"] = map[string]any{
		"auth_config":  map[string]any{"is_enabled": true, "admin_username": "env.BIFROST_ADMIN_USERNAME", "admin_password": "env.BIFROST_ADMIN_PASSWORD", "disable_auth_on_inference": false},
		"virtual_keys": vks,
		// Heimdall's own spend is capped at the gateway, like any consumer's.
		"budgets": []map[string]any{{"id": "budget-heimdall", "max_limit": opt.BudgetUSD, "reset_duration": "1d", "virtual_key_id": "vk-heimdall"}},
	}

	// Register Heimdall's MCP server on the gateway, so agents behind Bifrost
	// can ask about retirements and check answers. The gateway presents the
	// dashboard token when one is set.
	token := strings.TrimSpace(opt.Getenv("HEIMDALL_ADMIN_TOKEN"))
	if token == "" && res.Mode == ModeLive && strings.TrimSpace(opt.Getenv("HEIMDALL_ALLOW_ANONYMOUS")) == "" {
		token = sec.AdminToken
		res.Env["HEIMDALL_ADMIN_TOKEN"] = token
		res.Notes = append(res.Notes, "HEIMDALL_ADMIN_TOKEN was not set, so one was generated: "+token+" (stored in "+filepath.Join(abs, "heimdall-secrets.json")+"). Set your own to choose it.")
	}
	if !opt.NoMCP {
		client := map[string]any{"name": "heimdall", "connection_type": "http", "connection_string": strings.TrimRight(opt.HeimdallURL, "/") + "/mcp",
			"auth_type": "none", "tools_to_execute": []string{"*"}}
		if token != "" {
			client["auth_type"] = "headers"
			client["headers"] = map[string]string{"Authorization": "env.HEIMDALL_MCP_AUTHORIZATION"}
			res.Env["HEIMDALL_MCP_AUTHORIZATION"] = "Bearer " + token
		}
		cfg["mcp"] = map[string]any{"client_configs": []any{client}}
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(abs, "config.json"), append(data, '\n'), 0o644); err != nil {
		return res, err
	}

	res.Env["HEIMDALL_MODE"] = res.Mode
	res.Env["BIFROST_URL"] = opt.GatewayURL
	res.Env["BIFROST_ADMIN_USERNAME"] = adminUser
	res.Env["BIFROST_ADMIN_PASSWORD"] = adminPass
	res.Env["BIFROST_SETUP_TOKEN"] = firstNonEmpty(opt.Getenv("BIFROST_SETUP_TOKEN"), sec.SetupToken)
	res.Env["BIFROST_VIRTUAL_KEY"] = heimdallVK
	res.Env["HEIMDALL_VK"] = heimdallVK
	res.Env["HEIMDALL_TRAFFIC_KEYS"] = strings.Join(traffic[:], ",")
	if res.Tavily {
		res.Env["HEIMDALL_TAVILY_RELAY"] = "tavily"
	}
	return res, nil
}

// EnvFile renders the environment as KEY=VALUE lines, for an env file that
// both containers of a compose deployment load.
func (r BootstrapResult) EnvFile() string {
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s='%s'\n", k, strings.ReplaceAll(r.Env[k], "'", `'\''`))
	}
	return b.String()
}

// ShellExports renders the environment as POSIX export lines.
func (r BootstrapResult) ShellExports() string {
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s='%s'\n", k, strings.ReplaceAll(r.Env[k], "'", `'\''`))
	}
	return b.String()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
