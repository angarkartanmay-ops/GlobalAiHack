# Evidence rules

Heimdall asks a language model to read documentation and tell it when a model is being retired. Language models misquote, round dates, and answer from memory. A shutdown date that is wrong by a month is worse than no date, because someone will plan around it.

So the model's job is deliberately narrow: **point at the text.** Heimdall decides what the text means. This page states the rules exactly. Each one has a test.

## What counts as evidence

Two kinds of thing, and nothing else:

1. **Retrieved source text.** A passage from a page Tavily returned, with its URL.
2. **The provider's own API, read through the gateway.** `GET /v1/models/{id}` returns `shutdown_date` when a provider announces one.

Anything the analyst model says beyond that is **inference**. It is stored in a separate field and shown in a separate, labelled block. It never changes a status, a date or a confidence level.

## Rule 1: a quote must be in the source

A notice is kept only if its quote appears in the excerpt it cites.

The comparison is on canonical text: lowercase, with every run of punctuation, markdown and whitespace reduced to one space. Words and numbers are untouched. So a quote survives being reformatted (a table row quoted without its pipes) but not a changed word or a changed digit.

A quote shorter than 12 canonical characters is rejected. Short strings match by accident.

A notice that fails is **discarded**, counted, and reported: "its quote does not appear in the retrieved text".

`internal/evidence/match.go: Grounded` · tests: `TestGrounded`, `TestInventedQuoteIsDiscarded`

## Rule 2: a date must be in the quote

A shutdown date is used only if Heimdall's own date parser finds that date inside the quote.

If the analyst reports a date the quote does not contain, the notice is kept (the quote is real) but the date is dropped and the finding says so. A notice without a date can never be high confidence.

The parser accepts `2026-12-03`, `December 3, 2026`, `3 December 2026`, `Dec 3rd 2026` and month-only forms such as `March 2027`. It refuses `01/02/2026`: nobody can tell which field is the month, so Heimdall will not build a deadline on it.

A month-only date is recorded as the first day of that month, flagged as month precision, and capped at medium confidence.

`internal/evidence/dates.go` · tests: `TestFindDates`, `TestDateIn`, `TestInventedDateIsNotUsed`

## Rule 3: the analyst sees only passages that name the model

Provider lifecycle pages are long tables covering dozens of models. Sending a whole page invites the model to read the wrong row.

Heimdall cuts each page down first. For a table row it keeps the row, the table header and the nearest heading. For prose it keeps the sentence and two lines either side. The analyst never sees a neighbouring model's row, so it cannot quote one: if it tries, Rule 1 rejects it.

Model names are matched strictly:

| Text | Model | Match |
| --- | --- | --- |
| `gpt-4o will be retired` | `gpt-4o` | exact |
| `gpt-4o-mini will be retired` | `gpt-4o` | none: a different model |
| `GPT-4o mini is going away` | `gpt-4o` | none |
| `gpt-4.1 will be retired` | `gpt-4` | none |
| `gpt-4o-2024-05-13 will be retired` | `gpt-4o` | family: a dated snapshot of the alias |
| `Claude 3 Opus will be retired` | `claude-3-opus-20240229` | family: a display name |
| `my-gpt-4o-proxy` | `gpt-4o` | none |

A family match can support a finding but is flagged ("names an alias or snapshot") and capped at medium confidence.

`internal/evidence/match.go: Mention, Passages` · tests: `TestMention`, `TestPassagesTableRowKeepsHeaderAndHeading`, `TestNeighbouringModelsRowCannotBeUsed`

## Rule 4: status comes from the date

The analyst may call a model "deprecated" or "retired". Heimdall ignores the wording when it has a date: a shutdown date today or earlier means **retired**; a later one means **deprecated**.

Test: `TestPastDateMeansRetired`

## Rule 5: confidence comes from the source

| Confidence | Requires |
| --- | --- |
| High | An official source (the provider's documentation domain, or its API through the gateway) that names this exact model and gives a full date in the quote |
| Medium | Official, but an alias or a month-only date; or two independent third-party domains giving the same date; or official sources that disagree |
| Low | A single third-party source |

How certain the analyst sounded is not an input.

"Official" means the URL's host is on the provider's documentation list in the source registry, or a subdomain of one. `evil-platform.openai.com.example` is not `platform.openai.com`.

Tests: `TestThirdPartyCannotOutrankOfficial`, `TestRegistry`

## Rule 6: disagreement is shown, and the provider wins

- Official sources disagree: Heimdall plans for the **earliest** date, lowers confidence to medium, and reports the conflict.
- A third-party source disagrees with an official one: the official date stands and the conflict is reported.
- Only third-party sources: the earliest is used, at low confidence, with a note that no official confirmation was retrieved.

Tests: `TestOfficialSourcesDisagreeing`, `TestThirdPartyCannotOutrankOfficial`

## Rule 7: no notice is not "safe", and a failed check is not "no notice"

- Searched and found nothing that names the model: status **no notice found**, with the count of documents read. Not "active".
- "Active" needs a source that says so.
- Search or analyst failed: status **unknown**, with the error. If an earlier scan had a finding, that finding is kept rather than overwritten by a failure.

Test: `TestSearchFailureIsUnknownNotSafe`

## The same rules for answers

The stale-answer detector uses the same engine. A claim is marked **still true** or **out of date** only when the analyst's supporting quote is found in the search result it cites. Otherwise the claim is **could not verify**, whatever the analyst concluded.

Confidence is **high** only when a second result from a different domain states the same current value.

When a rehearsal finds two models disagreeing about a time-sensitive fact, the judge is instructed not to choose. Both claims go through this check against the same search results. The candidate wins only if its claim is supported and the baseline's is contradicted, and the reverse for a loss. Anything else is a draw.

`internal/freshness` · tests: `TestFreshnessEndpoint`, `TestRehearsalSeparatesADropInFromARewrite`

## What these rules do not do

They guarantee that every verdict rests on text that was retrieved. They do not guarantee the retrieved page is right, current, or complete. A provider that retires a model without updating its documentation will not be caught by reading its documentation. That is why the gateway's own `shutdown_date` signal is read first, why scans repeat, and why the safety-net routing rule exists.
