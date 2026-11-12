# Going live

Heimdall decides its mode from the environment at startup:

- **No provider key**: simulator mode. A real Bifrost gateway, with simulated providers and a simulated web. Needs nothing.
- **A provider key**: live mode. Real providers, the real Tavily API, a real model reading the evidence.

Switching is a matter of setting variables and restarting. Nothing else changes.

## The values you need

| Variable | Where to get it | Required |
| --- | --- | --- |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `MISTRAL_API_KEY` or `OPENROUTER_API_KEY` | Your provider's console | At least one |
| `TAVILY_API_KEY` | <https://app.tavily.com> | Yes, for the radar and the answer check |
| `HEIMDALL_ADMIN_TOKEN` | Choose any long random string | Yes. It is the dashboard password |
| `HEIMDALL_ANALYST_MODEL` | A small chat model the gateway serves, as `provider/model` | No. Chosen automatically |
| `HEIMDALL_ALERT_WEBHOOK_URL` | A Slack incoming webhook | No |

Two or more provider keys make rehearsals more useful: you can test a replacement from a different provider, and the judge can be a different model from the two it is comparing.

## Option 1: one container

Works on any host that runs a Docker image and publishes one port: a VM, Render, Railway, Fly.io, Cloud Run, Hugging Face Spaces, Vercel's container runtime.

```bash
docker build -t heimdall .
docker run -d --name heimdall -p 7860:7860 \
  -e OPENAI_API_KEY=... \
  -e ANTHROPIC_API_KEY=... \
  -e TAVILY_API_KEY=... \
  -e HEIMDALL_ADMIN_TOKEN=... \
  -v heimdall-data:/app/data \
  heimdall
```

The container listens on `$PORT` (default 7860). The gateway inside it is bound to localhost and is not published.

Without the volume, state is lost on restart: findings are rebuilt by the next scan, but routing rules and the gateway's logs are gone. Mount `/app/data` for anything you intend to keep.

To also reach the gateway's own dashboard on the same port, add `-e HEIMDALL_EXPOSE_GATEWAY=true -e BIFROST_ADMIN_PASSWORD=...`. It appears under `/gateway/` and asks for the user `heimdall` and that password.

### Vercel

The hosted demo runs on Vercel's container runtime, which builds the root `Dockerfile`. To take that deployment live, add the keys from the table above as environment variables on the Vercel project and redeploy.

For a hackathon or any public showcase, run two Vercel projects from the same repository: one with no keys, which stays in simulator mode and open to everyone, and one with your keys and `HEIMDALL_ADMIN_TOKEN`, which stays locked. Share only the first. That is how <https://heimdall-sim.vercel.app> is set up.

Vercel recycles idle containers and gives them no persistent disk. That suits a demo, but in live use it means the gateway's logs and any routing rule Heimdall created disappear whenever the container is recycled. For anything you rely on, use a host with a persistent volume.

### Render

`render.yaml` in this repository is a Render Blueprint. In Render choose **New → Blueprint**, pick this repository, and fill in the keys it asks for. Leave them all empty to deploy the simulator.

### Hugging Face Spaces

Create a Space with the **Docker** SDK and push this repository to it. The `deploy-space` workflow does that for you on every push once two things are set on the GitHub repository:

- secret `HF_TOKEN`: a Hugging Face token with write access
- variable `HF_SPACE`: the Space as `owner/name`

Then set the keys from the table above as Space secrets.

## Option 2: next to a gateway, with compose

```bash
cp .env.example .env    # fill in the keys
docker compose up --build
```

Heimdall is on <http://localhost:8787> and Bifrost on <http://localhost:8080>. The gateway is the official image; it starts after Heimdall has written its configuration.

## Option 3: a gateway you already run

Heimdall needs four things from an existing Bifrost (v2.0 or later; relays need a current release):

1. **A virtual key for Heimdall**, allowed to use the providers it should rehearse against. Put a budget on it.
2. **The admin login**, for logs and routing rules.
3. **Content logging on** (the default), so there are requests to replay.
4. **Tavily as a relay**, granted to that virtual key:

```json
{
  "relays": [{
    "name": "tavily",
    "base_url": "https://api.tavily.com",
    "auth": { "type": "bearer" },
    "credential": { "value": "env.TAVILY_API_KEY" },
    "routes": [
      { "method": "POST", "path": "/search" },
      { "method": "POST", "path": "/extract" }
    ]
  }],
  "governance": {
    "virtual_keys": [{
      "id": "vk-heimdall", "name": "heimdall", "value": "env.HEIMDALL_VK",
      "allow_all_providers": true,
      "relay_grants": [{ "relay_name": "tavily" }]
    }],
    "budgets": [{ "id": "budget-heimdall", "max_limit": 5, "reset_duration": "1d", "virtual_key_id": "vk-heimdall" }]
  }
}
```

Then run Heimdall alone:

```bash
docker build -t heimdall --target heimdall .
docker run -d -p 8787:8787 -v heimdall-state:/data \
  -e BIFROST_URL=http://your-gateway:8080 \
  -e BIFROST_VIRTUAL_KEY=sk-bf-... \
  -e BIFROST_ADMIN_USERNAME=... -e BIFROST_ADMIN_PASSWORD=... \
  -e HEIMDALL_ADMIN_TOKEN=... \
  heimdall
```

If you would rather not use a relay, leave `HEIMDALL_TAVILY_RELAY` unset and give Heimdall `TAVILY_API_KEY`: it then calls Tavily directly.

`heimdall bootstrap --gateway-dir ./out` prints a complete working `config.json` for your environment, which is the easiest way to see every setting in place.

## First run checklist

1. Open the dashboard and enter `HEIMDALL_ADMIN_TOKEN`.
2. **Setup**: every row under Connections should be green. A red row says what is wrong.
3. If the gateway is new, **Setup → Send sample traffic** and choose models. These are real, billed requests: about a hundred short ones.
4. **Watch → Scan now**.
5. Open a model with a notice, check the quoted evidence against the linked page, and rehearse the replacement.

## What to check on the first live run

Live mode was built and tested against a real Bifrost gateway, but the build environment could not reach real providers or the real Tavily API. Three things are therefore worth a deliberate look the first time:

- **Tavily results.** Open a finding and follow the source link. The quote should be on that page.
- **The analyst model.** Setup shows which model was chosen. If its JSON is unreliable, set `HEIMDALL_ANALYST_MODEL` to a stronger one. A model that misquotes does not produce wrong findings (its notices are discarded), it produces fewer.
- **Lifecycle page locations.** If a provider has moved its deprecations page, the page read fails quietly and the search covers for it. `HEIMDALL_SOURCES_FILE` lets you correct the list without rebuilding.

## Costs

Per scan: one Tavily extract per provider, two advanced searches and one analyst call per model. Twenty models is roughly forty searches.

Per rehearsal: one replay per request on the candidate, plus one judge call for each pair that is not identical, plus one search and two analyst calls per time-sensitive disagreement (capped at six). The rehearsal stops at `HEIMDALL_REHEARSAL_BUDGET_USD`, and the gateway stops Heimdall at its virtual key's budget regardless.
