# Architecture

Heimdall is one Go binary with the dashboard embedded. It sits beside a Bifrost gateway and reaches everything else through it.

```
cmd/heimdall          entry point: serve, bootstrap
internal/
  bifrost             client for the gateway's public HTTP surface
  tavily              Tavily Search and Extract, via a Bifrost relay or direct
  analyst             Heimdall's own model calls, through the gateway
  evidence            dates, model-name matching, passages, grounding, source registry
  radar               inventory from gateway logs; lifecycle findings per model
  freshness           the stale-answer detector
  rehearsal           sample, replay, compare, judge, settle with live evidence
  cutover             migrations as Bifrost routing rules
  store               embedded key-value store for Heimdall's conclusions
  server              configuration, bootstrap, scheduler, HTTP API
  mcpserver           Heimdall as an MCP server
  sim                 simulated providers and web, for keyless runs and tests
  testgw              in-memory double of the gateway API, for hermetic tests
web/                  dashboard (React, Vite), built into web/dist and embedded
e2e/                  suite that runs against a real Bifrost binary
```

## One boundary

Heimdall has exactly one outbound dependency: the gateway.

| Heimdall needs | It calls |
| --- | --- |
| Traffic by model | `GET /api/logs/rankings` |
| Traffic by application | `GET /api/logs/rankings/by-dimension?dimension=virtual_key` |
| Real requests to replay | `GET /api/logs`, `GET /api/logs/{id}` |
| A model's metadata | `GET /v1/models`, `GET /v1/models/{id}` |
| A model to read evidence or judge | `POST /v1/chat/completions` with `fallbacks` |
| Web search and page text | `POST /relay/tavily/search`, `/relay/tavily/extract` |
| To move traffic | `/api/routing/rules` |

Consequences:

- **One credential set.** A virtual key (inference, relay) and the gateway's admin login (logs, routing). No provider keys, no Tavily key.
- **Heimdall is governed like any consumer.** Its spend counts against a budget on its virtual key. If the gateway says no, Heimdall stops.
- **Everything Heimdall does is in the gateway's logs.** Analyst calls, replays and searches are all visible there.

## Keeping its own traffic out

Rehearsals send hundreds of requests to the replacement model. If those counted as demand, the replacement would show up as "in use" and could be sampled into its own rehearsal.

Every request Heimdall sends carries `x-bf-lh-heimdall: 1`. Bifrost captures `x-bf-lh-*` headers into log metadata. Heimdall then:

- subtracts `metadata_heimdall=1` aggregates from the inventory, per model and per virtual key;
- skips any sampled log carrying the tag.

This is tested against the real gateway: after a scan and a rehearsal, neither the analyst model nor the rehearsed candidate appears in the inventory.

## The scan

```
RunScan
  inventory        gateway aggregates, minus Heimdall's own
  for each model (three at a time, most traffic first):
    gateway signal   GET /v1/models/{id} -> shutdown_date?
    lifecycle pages  Tavily extract, once per provider per scan
    search           Tavily, restricted to the provider's domains
    open search      Tavily, excluding them (optional)
    passages         cut each document down to text naming this model
    analyst          one call: which passages state a notice?
    ground           keep notices whose quote is in the passage; dates must be in the quote
    conclude         status, date, confidence, conflicts
    resolve          map the named replacement onto a model the gateway serves
    compare          record what changed since the last scan
  events           new notices, moved dates, countdown thresholds -> activity feed and webhook
```

Cost per scan: one extract per provider, two searches and one analyst call per model. Sixty models is the ceiling per scan, taken in order of traffic.

## A rehearsal

```
collect     recent successful chat requests to the baseline, from gateway logs
            round-robin across virtual keys; distinct by conversation and tools
replay      each against the candidate, with the logged parameters; four at a time
            stops at the rehearsal budget, or when the gateway's budget refuses
compare     deterministic checks: answered, tool calls, JSON, truncation, refusal
settle      hard failure -> regression
            identical -> equivalent, no model call
            otherwise  -> judge model
              time-sensitive disagreement -> live evidence decides
score       share of requests that did not regress; ready / ready with caveats / not ready
```

The deterministic checks run before any model is asked, for two reasons. They are the failures that break applications outright. And a parser's verdict on "this JSON does not parse" needs no second opinion.

## A migration

One routing rule per retiring model, named `heimdall-<provider>-<model>`, in global scope, at the first free priority from 100. Heimdall creates it, updates it between stages, and deletes it on rollback. It never touches a rule it did not name.

Canary health is read from gateway logs filtered by the rule's ID, counting only requests since the current stage began. A guard trips when the replacement succeeds on under 80% of at least 20 requests, or more than ten points below the current model. It raises a critical event and, if configured, rolls back.

## State

`heimdall.db` (bbolt, one file) holds findings, scans, rehearsals, migrations, freshness reports and events. It does not hold traffic. Losing it loses history, not safety: the next scan rebuilds findings, and routing rules live on the gateway.

## Modes

`heimdall bootstrap` writes the gateway's `config.json` and decides the mode from the environment:

- **Simulator**: no provider key. Providers `aurora` and `lumen` are custom OpenAI-compatible providers pointing at Heimdall's `/sim` endpoints; the `tavily` relay points at a simulated search API over a dozen invented pages. The analyst is a rule-based stand-in.
- **Live**: at least one provider key. Real providers, the real Tavily API behind the relay, a real model as analyst. The simulator is not mounted.

In both, secrets are referenced as `env.NAME` in `config.json` and never written into it.

The simulator is behind the gateway on purpose. In simulator mode the gateway is still real: routing rules, fallbacks, virtual keys, budgets, logs, relays and MCP are all exercised for real. Only the two things that need paid keys are replaced.

## Access

- Simulator mode, no token: open. There is nothing real behind it.
- Live mode: `HEIMDALL_ADMIN_TOKEN` is required. Without it the API refuses every request rather than running open, because the dashboard shows real request content and can change routing.

## Scaling

Heimdall does no per-request work. It is not in the request path, so it adds no latency and cannot take traffic down. Its load is proportional to the number of distinct models, not to request volume, because the gateway aggregates. The one growth point is rehearsal sampling, which pages at most 500 log entries per run.
