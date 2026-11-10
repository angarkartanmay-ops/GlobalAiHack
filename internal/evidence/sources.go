package evidence

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
)

// Source describes where a provider publishes model lifecycle notices.
type Source struct {
	// Domains are the provider's own documentation hosts. Evidence from these
	// is "official"; anything else is third-party and can never, on its own,
	// produce a high-confidence finding.
	Domains []string `json:"domains"`
	// Pages are lifecycle pages worth reading in full on every scan. They are
	// a starting point, not a dependency: a page that has moved simply fails
	// to extract, and the domain-restricted search still finds its successor.
	Pages []string `json:"pages"`
	// Aliases are other provider names that share this entry.
	Aliases []string `json:"aliases,omitempty"`
}

// Registry maps a Bifrost provider name to its lifecycle sources.
type Registry map[string]Source

// DefaultRegistry covers the providers Bifrost supports out of the box.
// Operators override or extend it with HEIMDALL_SOURCES_FILE.
func DefaultRegistry() Registry {
	return Registry{
		"openai": {
			Domains: []string{"platform.openai.com", "developers.openai.com", "openai.com", "help.openai.com"},
			Pages:   []string{"https://platform.openai.com/docs/deprecations"},
		},
		"anthropic": {
			Domains: []string{"docs.anthropic.com", "platform.claude.com", "docs.claude.com", "anthropic.com", "support.anthropic.com"},
			Pages:   []string{"https://docs.anthropic.com/en/docs/about-claude/model-deprecations"},
		},
		"gemini": {
			Domains: []string{"ai.google.dev", "developers.googleblog.com"},
			Pages:   []string{"https://ai.google.dev/gemini-api/docs/deprecations"},
			Aliases: []string{"google", "genai"},
		},
		"vertex": {
			Domains: []string{"cloud.google.com", "docs.cloud.google.com"},
			Pages:   []string{"https://cloud.google.com/vertex-ai/generative-ai/docs/deprecations"},
		},
		"azure": {
			Domains: []string{"learn.microsoft.com", "azure.microsoft.com"},
			Pages:   []string{"https://learn.microsoft.com/en-us/azure/ai-foundry/openai/concepts/model-retirements"},
		},
		"bedrock": {
			Domains: []string{"docs.aws.amazon.com", "aws.amazon.com"},
			Pages:   []string{"https://docs.aws.amazon.com/bedrock/latest/userguide/model-lifecycle.html"},
		},
		"mistral": {
			Domains: []string{"docs.mistral.ai", "mistral.ai"},
			Pages:   []string{"https://docs.mistral.ai/getting-started/models/models_overview/"},
		},
		"groq": {
			Domains: []string{"console.groq.com", "groq.com"},
			Pages:   []string{"https://console.groq.com/docs/deprecations"},
		},
		"cohere": {
			Domains: []string{"docs.cohere.com", "cohere.com"},
			Pages:   []string{"https://docs.cohere.com/docs/deprecations"},
		},
		"cerebras": {
			Domains: []string{"inference-docs.cerebras.ai", "cerebras.ai"},
			Pages:   []string{"https://inference-docs.cerebras.ai/support/deprecation"},
		},
		"xai":        {Domains: []string{"docs.x.ai", "x.ai"}},
		"deepseek":   {Domains: []string{"api-docs.deepseek.com", "deepseek.com"}},
		"perplexity": {Domains: []string{"docs.perplexity.ai", "perplexity.ai"}},
		"openrouter": {Domains: []string{"openrouter.ai"}},
		"fireworks":  {Domains: []string{"docs.fireworks.ai", "fireworks.ai"}},
		"together":   {Domains: []string{"docs.together.ai", "together.ai"}},
		"nebius":     {Domains: []string{"docs.nebius.com", "nebius.com"}},
	}
}

// LoadRegistry returns the default registry merged with the JSON file at
// path, when path is not empty. Entries in the file replace defaults of the
// same name.
func LoadRegistry(path string) (Registry, error) {
	reg := DefaultRegistry()
	if path == "" {
		return reg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return reg, err
	}
	var extra Registry
	if err := json.Unmarshal(data, &extra); err != nil {
		return reg, err
	}
	for k, v := range extra {
		reg[strings.ToLower(k)] = v
	}
	return reg, nil
}

// For returns the source entry for a provider, following aliases.
func (r Registry) For(provider string) (Source, bool) {
	p := strings.ToLower(provider)
	if s, ok := r[p]; ok {
		return s, true
	}
	for _, s := range r {
		for _, a := range s.Aliases {
			if a == p {
				return s, true
			}
		}
	}
	return Source{}, false
}

// DomainOf returns the lowercase host of a URL without a leading "www.".
func DomainOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// Official reports whether a URL is on the provider's own documentation
// hosts (the host itself or a subdomain of a listed host).
func (r Registry) Official(provider, rawURL string) bool {
	src, ok := r.For(provider)
	if !ok {
		return false
	}
	d := DomainOf(rawURL)
	if d == "" {
		return false
	}
	for _, od := range src.Domains {
		if d == od || strings.HasSuffix(d, "."+od) {
			return true
		}
	}
	return false
}
