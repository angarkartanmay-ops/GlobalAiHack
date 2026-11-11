# Demo script

About three and a half minutes. Works in simulator mode with no keys, so it can be recorded from a fresh `docker run`. Lines in quotes are what to say.

Start the container a minute before recording so the first scan has finished:

```bash
docker run --rm -p 7860:7860 heimdall
```

## 0:00 The problem (20 s)

Show the watch.

> "Providers retire models on a schedule. The notice goes to whoever made the account, and the first the application team hears is a 404 in production. The gateway knows exactly which models we depend on. Nothing connects those two facts. Heimdall does."

## 0:20 The watch (35 s)

Point at the four figures, then the horizon.

> "This is every model carrying traffic through this Bifrost gateway, read from the gateway's own logs. Four of five are scheduled for shutdown. The big one, `aurora-large-1`, carries about eighty percent of our requests and has 23 days left. One is already shut down and a forgotten batch job is still calling it."

Hover the horizon marks.

> "Position is time left. Size is how much traffic rides on it."

## 0:55 Evidence (45 s)

Click `aurora-large-1`.

> "Every claim here is quoted from a page Tavily retrieved, with the link. The model that read the page is not trusted with the conclusion. Heimdall only keeps a notice if the quote is found word for word in the source, and only uses a date it can parse out of that quote."

Point at the conflict banner.

> "A third-party blog says January. The provider's own documentation says December. Heimdall shows the disagreement and goes with the provider."

Go back, open `aurora-mini-1`, point at the discarded notice.

> "Here the model paraphrased a forum post instead of quoting it. That notice was thrown out, and it says so."

## 1:40 Rehearsal (55 s)

Back to `aurora-large-1`. Press **Rehearse**.

> "The provider names a replacement. Is it safe for our traffic? Heimdall takes real requests from the gateway's logs and replays them against the candidate. Production is untouched, and the spend is capped by a budget on the gateway."

When the report appears:

> "Forty real requests. Cheaper and faster. A few regressed, and none of them needed a model to spot: JSON output wrapped in a markdown fence, and an order number sent as an integer where the tool expects a string."

The exact counts depend on which requests were sampled; read them off the screen.

Click **Settled by evidence**, open the first.

> "And this is where the stale-answer detector comes in. The two models disagree about a fact that changes over time. Neither is assumed right. Heimdall checks both claims against the web today. The current model's answer is out of date; the replacement's is correct. That counts in its favour."

## 2:35 Cutover (45 s)

Back to the model. Press **Arm safety net**.

> "A migration is one Bifrost routing rule. First a safety net: the old model keeps serving, the replacement is its fallback."

Press **Shut it down** under *Simulate the shutdown*.

> "Now the provider retires the model."

Open the watch in a second tab, or wait for the health panel.

> "Requests are still succeeding, served by the replacement. Without the rule, every one of these would be a 404."

Press **Bring it back**, then **Start canary at 10%**, then show the health panel.

> "Then a canary on live traffic, with health measured from the gateway's logs. Then cut over. Applications keep sending the old model name and never change."

## 3:20 Close (15 s)

Show **Answer check** with the example, or **Setup**.

> "It runs on a stock Bifrost image, through public endpoints only: logs, routing rules, virtual keys, relays and MCP. Add a provider key and a Tavily key and the same container runs live."

## If recording live mode

- Use **Setup → Send sample traffic** first, choosing at least one model that is known to be deprecated, then **Scan now**.
- Open the source link on a finding to show the quote on the provider's real page.
- Use **Answer check** with an answer about something that changed recently.
