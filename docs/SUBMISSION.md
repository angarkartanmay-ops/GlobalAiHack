# Submission notes

Text for the HackCulture submission form, ready to paste and edit.

## Project name

Heimdall

## One line

Heimdall watches a Bifrost gateway for models that are about to be retired, proves a replacement on your real traffic, and cuts over before anything breaks.

## Problem statements

- **Bifrost: Build a Feature the Open-Source Gateway Is Missing** (primary)
- **Tavily: Build a Stale-Answer Detector for AI Applications** (combined into the same product)

## Short description (about 100 words)

Model providers retire models on a schedule, and the first most teams hear of it is a 404 in production. A gateway knows exactly which models an organisation depends on, but Bifrost only notices a retired model after requests fail. Heimdall closes that gap. It reads which models carry traffic from Bifrost's logs, finds retirement notices in the provider's own documentation through Tavily, replays real logged requests against the replacement to prove it is safe, and moves traffic with a staged, reversible Bifrost routing rule. Every finding is backed by source text quoted word for word.

## What makes it different

- **It is about stale configuration, not just stale answers.** The models in your routing config go out of date the same way facts do. Nobody was watching that.
- **The model is not trusted with the conclusion.** A notice counts only if its quote is found verbatim in the retrieved page, and a date counts only if it can be parsed out of that quote. A paraphrase or an invented date is discarded, visibly.
- **Rehearsal uses your traffic.** Real requests from the gateway's logs, replayed against the candidate. Tool-call and JSON regressions are caught by a parser, not by a model's opinion.
- **The stale-answer detector has a job inside the product.** When the old and new model disagree about a fact that changes over time, neither is assumed right: live evidence decides.
- **No fork.** It runs on the official Bifrost image and uses only public endpoints.

## How Bifrost is used

Logs and aggregates (inventory, sampling, canary health), routing rules (safety net, canary, cutover, rollback), virtual keys and budgets (Heimdall's own spend), request fallbacks, relays (Tavily behind the gateway), logging headers (tagging its own traffic), the model API (`shutdown_date`), and the MCP gateway (Heimdall's tools for agents).

## How Tavily is used

Extract for provider lifecycle pages; domain-restricted Search per model; an open Search for corroboration; Search per time-sensitive claim in the stale-answer detector and to settle disagreements in rehearsals. All through a Bifrost relay.

## Tech

Go (one static binary, dashboard embedded), React and TypeScript for the dashboard, bbolt for state. Docker image built on `maximhq/bifrost`.

## Links

- Repository: https://github.com/angarkartanmay-ops/GlobalAiHack
- Live demo (simulator mode): https://heimdall-watch-six.vercel.app
- Demo video: to be added
- Deck: `docs/Heimdall.pptx` in the repository

## Testing and verification, stated plainly

Unit, integration and end-to-end tests are in the repository. The end-to-end suite runs against a real Bifrost binary. Live mode has been exercised against a real gateway up to the provider boundary; the first run with real provider and Tavily keys is the first test of those two integrations.

## AI assistance

Built with AI assistance (Claude Code).
