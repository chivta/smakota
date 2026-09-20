**PRD — Calorie logging bot (working name: TBD)**

**Goal**
One user can log a full day of food in under 2 minutes total, in Ukrainian, including homemade dishes, and keep doing it past week 4.

**Primary user**
Ukrainian-speaking adult on a cut, non-technical, phone-only, already lives in Telegram. Eats mostly homemade dishes, some packaged goods.

**Non-goals for v1**
Meal-type separation, exercise, water, recipes, micronutrients, macro charts beyond protein, social features, multi-language, custom food creation UI, plate-photo portion estimation.

---

**Architecture constraint**
Backend is an HTTP API with no Telegram-specific logic. Telegram bot and Mini App are clients. The Mini App is plain web, servable standalone later.

Stack: Go API, PostgreSQL as the only durable store, Redis for the resolver's hot cache and rate limiting, React + TypeScript SPA for the Mini App. Ingestion workers are the same Go binary invoked with a subcommand, so adapters share the normalizer and the DB layer with the API.

---

**Food database (this is the core deliverable, not infrastructure)**

Two entity types:

| Type | Source | Keyed by | Portion model |
|---|---|---|---|
| Packaged product | barcode DBs + retailer APIs + label OCR | barcode / name | per 100 g + package size |
| Dish / raw food | curated Ukrainian list | name + aliases | per 100 g + named default portions |

Requirements:
- Every dish entry has at least one named default portion with grams ("тарілка", "шматок", "склянка", "порція").
- Every entry that can be raw or cooked stores which state the values describe, and it's shown in the UI.
- Minimum viable coverage: the 300 most common Ukrainian home dishes and raw ingredients, verified by hand. Not scraped-and-hoped.
- Aliases per entry in Ukrainian and Russian, plus common misspellings and voice-transcription errors.
- Every user query that fails to resolve, or gets refined, is persisted to a review queue. That queue drives DB expansion.
- Every row carries `source_id`, `source_url`, `fetched_at`, `license_class` and `confidence`. Provenance is not optional metadata; it decides whether a row may be exported and whether it may overwrite another.

**Source tiers**

Findings from `.claude/nutrition-sources` (`sources.json`, `deadends.json`, `research/`). There is no Ukrainian national food composition table. Nothing in the research turned one up, so the dish layer is hand-built from foreign composition tables plus per-dish recipe math. Plan for that instead of hunting for a local table again.

*Tier 1, redistributable, automated:*

| Source | What it gives | License | Refresh |
|---|---|---|---|
| Open Food Facts | Barcode backbone. 4.7M products worldwide, 7,286 tagged Ukraine. JSON API plus full dumps in JSONL/Parquet | ODbL for the DB, DbCL for contents, attribution required | Daily delta, weekly full dump |
| USDA FoodData Central | Generic and raw food fallback, 300k+ branded items. Bulk CSV | CC0, no strings | Quarterly |
| UK CoFID | Cooked-state values for meat, grains, vegetables. ~2,890 rows, XLSX | Open Government Licence, attribution | Manual, on release |
| French CIQUAL | Second opinion on cooked states, 3,484 foods, XLSX | Licence Ouverte | Manual, on release |

CoFID and CIQUAL exist in this list for one reason: they publish raw and cooked rows separately, which is exactly what the "варена курка" case needs and what no retailer catalogue provides.

*Tier 2, lookup-only, automated:*

| Source | What it gives | Access | Refresh |
|---|---|---|---|
| Сільпо | ~71,600 food items with per-100 g kcal and macros | `GET sf-ecom-api.silpo.ua/v1/uk/branches/{branch}/products/{externalProductId}`, no key. Use the numeric `externalProductId`, not the UUID | Weekly |
| Zakaz.ua | Novus, Auchan, METRO, ЕКО, Таврія В, Megamarket under one JSON API, ~16.8k items per branch | JSON API, no key | Weekly |
| Varus | ~81k sitemap URLs, nutrition in an embedded JSON blob keyed `energyKcal`/`protein`/`fat`/`carb` | HTML. Real sitemap is `varus.ua/media/sitemap/sitemap.xml`; the path in robots.txt 404s | Weekly |
| Producers matching the diary | Яготинське (843 URLs), Комо, Чумак, Торчин, Верес, Молокія, Power Pro, BioTechUSA | HTML | Monthly |
| Foodservice | McDonald's UA (22-page PDF), KFC UA, Євразія, Пузата Хата, Sushi Master | PDF and embedded JSON | Monthly |
| Bakery and fuel-station food | Київхліб (281 products), Хлібний (126 kcal-bearing items), Три ведмеді (~126), Львівські круасани | HTML | Monthly |

Tier 2 terms forbid reproduction. These rows are queried by the resolver and rendered to the user one at a time; they are never bulk-exported, never served from a public data endpoint, and never included in a DB dump that leaves the host. `license_class` enforces this in code, not in a comment.

*Not in v1:* every paid API (FatSecret, Nutritionix, Edamam, Spoonacular, Chomp, Go-UPC, EAN-Search). One user does not justify a contract, and the free tiers all demand visible attribution in the UI. Revisit only if barcode miss rate stays above 20% after OFF and Сільпо are both live.

*Do not retry:* `deadends.json` records ~50 sources already checked and rejected, with reasons. АТБ and Rozetka are Cloudflare-403. Фора, SOCAR and Bolt Food are empty app shells. Domino's, Glovo, Celentano and Хінкальня expose menu JSON with no calorie fields or with kcal hardcoded to 0. Read that file before adding any adapter.

**Seeding the 300 dishes**
The diary in `research/products.txt` is the priority list, not a guess. Its top rows (Яготинський Молоко 1%, Snickers, куряче філе, Non Stop, BioTechUSA, соєвий соус, Lay's, сир Гауда, індиче м'ясо запечене) must all resolve on day one. Work down that file first, then fill out the standard home-dish set (борщ, вареники, гречка, котлети, олів'є, сирники) by hand from CoFID and CIQUAL ingredient values with recipe weights.

---

**Ingestion pipeline**

One adapter per source, each implementing `Fetch → Normalize → Stage`. Adapters never write to live tables.

1. **Fetch.** Per-source rate limit and a persisted cursor, so a killed job resumes rather than restarts. HTTP responses are cached by ETag where the source sends one.
2. **Normalize.** Everything converts to per-100 g. kJ converts to kcal. Comma decimals, `<0.5`, `0,5 г` and ranges all parse or the row is rejected with a reason.
3. **Validate.** Atwater check: reject when `|kcal - (4·protein + 9·carbs + 4·fat)|` exceeds 20% of the stated kcal. Reject macros summing above 100 g. Reject kcal above 900. Rejections go to the review queue with the raw payload attached, not to a log line nobody reads.
4. **Stage.** Rows land in `staging_items` with a content hash. Unchanged rows are skipped.
5. **Promote.** A diff step applies staged rows to live entries. A new value never silently overwrites a higher-confidence one: Tier 1 beats Tier 2, and a hand-verified row beats both. Conflicts above a threshold queue for review instead of applying.
6. **Reindex.** Search index and alias table rebuild after any promote that changed rows.

Each run writes a row to `ingest_runs` with source, started/finished, counts fetched, staged, rejected, promoted, and the error if it failed. That table is the answer to "is the data fresh", and `/metrics` exposes its age per source.

---

**Input flows**

All four inputs converge on the same resolver: `input → candidate list (ranked, with confidence) → confirm → portion → save`.

1. **Text** — free form, e.g. "варена курка 150г". Parser extracts food name + optional quantity/unit. If quantity is present, skip the portion step and go straight to the save confirmation.
2. **Voice** — Telegram voice message, transcribed, then identical to text.
3. **Photo** — packaging label only. OCR → product name + per-100 g values. If the label parses cleanly, create/match an entry and go to portion.
4. **Barcode** — scanned in the Mini App camera. Lookup order: local entries, then Open Food Facts, then the retailer catalogues. On miss, offer the label-photo flow, and push the barcode to the review queue.

**Resolution behaviour**
- High confidence: show one result with the portion field pre-filled. One tap saves.
- Low confidence or tie: show up to 5 candidates.
- Always available: "не те" → user adds clarification by text or voice → re-resolve with the original input plus the clarification as context.
- After 2 failed refinements: offer manual kcal-per-100g entry, save it as a personal entry, and push the query to the review queue.

**Portion step**
- Pre-fill in this priority order: this user's last amount for this food → the entry's default named portion → 100 g.
- Accept either grams or a count of a named portion.
- One screen, numeric input plus 2–3 named-portion buttons.

**Recents**
Quick-add keyboard showing the user's 8 most-logged foods from the last 14 days, each re-logging at that user's last-used amount in one tap. Plus "повторити вчорашній день".

---

**Day view**
Flat chronological list, no meal grouping. Each row: food, amount, kcal. Tap to edit amount or delete. Header shows consumed / target and remaining kcal, plus protein consumed vs. target. No day-close lock — past days stay editable for 7 days.

**Onboarding**
Collect sex, age, height, current weight, goal weight, activity level. Compute maintenance and a deficit target, show both numbers with a one-sentence rationale, allow manual override. Set protein target as g/kg of bodyweight. Total: one screen, under 60 seconds.

**Weight tracking**
Optional weight entry, any frequency. Display only as a 7-day rolling average trend versus target rate. Never show day-over-day deltas.

**Notifications**
One evening nudge if the day has zero entries, at a user-set time. One weekly summary. Both individually disableable. Nothing else, ever.

**Copy rules**
Neutral, non-judgmental Ukrainian. No red "over budget" states, no shaming for missed days, no streak-loss messaging. Going over target renders the same as going under.

---

**Deployment**

Single host, `docker compose up -d` from a checked-out repo. No orchestrator in v1. One user and ten scheduled jobs do not need a cluster, and the compose file is something the whole stack can be read off in one screen. Revisit if a second host or zero-downtime deploys ever matter.

The compose file at the repo root is the ground truth for what runs. Images are tagged with the full commit SHA; CI builds, tests and lints, then builds and pushes only the components that changed, rewrites the tags in the compose file and commits the result with a `deploy:` prefix. Deploying is `git pull && docker compose up -d` on the host.

Services:

| Service | Notes |
|---|---|
| `api` | HTTP API, serves the Mini App bundle. Migrations run on startup, no separate step |
| `bot` | Telegram client, long polling. One replica, since a token allows one consumer |
| `scheduler` | Same image, `scheduler` subcommand. Owns every recurring job below |
| `postgres` | Named volume, scheduled `pg_dump` to off-host storage |
| `redis` | Cache and rate limiting only, never a source of truth |
| `caddy` | TLS termination and reverse proxy. Automatic certificates, one line per route |

`seed` is a one-shot, re-runnable Tier 1 bulk load, run with `docker compose run --rm api seed`.

The scheduler is an in-process cron in the Go binary, not host crontab and not a sidecar like ofelia: it shares the config, the logger and the DB pool with everything else, and one `ingest_runs` row records each attempt. Each job takes a Postgres advisory lock before doing work, so a run that overlaps its successor is skipped rather than duplicated, and each has a timeout sized to its source.

| Schedule | Job | What it does |
|---|---|---|
| Daily 03:00 | `ingest-off-delta` | Open Food Facts delta, Ukraine tag first |
| Weekly Sun 02:00 | `ingest-off-full` | Full OFF dump reconciliation |
| Weekly Mon 02:00 | `ingest-silpo` | Сільпо commerce API |
| Weekly Tue 02:00 | `ingest-zakaz` | Zakaz.ua, one pass per retailer branch |
| Weekly Wed 02:00 | `ingest-varus` | Varus sitemap crawl |
| Monthly 1st | `ingest-producers` | Producer sites from the diary list |
| Monthly 2nd | `ingest-foodservice` | Restaurant and bakery menus, PDF parsing |
| Quarterly | `ingest-reference` | USDA, CoFID, CIQUAL refresh. Disabled by default, triggered by hand |
| Daily 06:00 | `review-digest` | Collapses the review queue into one message |
| Every 10 min | `notifier` | Sends evening nudges and weekly summaries whose time has come |

Staggered on purpose: two catalogue crawls never run at once, so one source's rate limit cannot starve another. Every job is also invocable directly for a manual run, as `docker compose run --rm api ingest <name>`.

Every service exposes `GET /health` returning 204 and `GET /metrics`. Logging middleware skips both. Logging is structured and leveled, fields are key-value pairs, errors sit in a dedicated field, and an error-level event increments the matching counter. Config comes from environment variables into a typed object validated at startup; the process exits on anything missing or invalid. A `.example.env` documents every variable and is committed; the real `.env` lives on the host and is gitignored. Containers run non-root with a read-only filesystem and all capabilities dropped.

Dockerfiles have three named stages. Dev runs with hot reload against bind-mounted source. Builder compiles the artifact. Production starts from distroless and carries the binary and nothing else. `compose.yaml` plus `compose.dev.yaml` covers both: the dev overlay swaps in the dev stage and the bind mounts.

---

**Acceptance criteria**

- Median time from starting input to saved entry: under 15 s for voice and text; under 10 s for recents.
- First-candidate acceptance rate on the user's real diet: ≥ 70%.
- Unresolved-food rate: ≤ 10% of attempts.
- Every input attempt is logged with input type, latency, whether the first candidate was accepted, and whether refinement was used.
- No ingest source is older than twice its schedule, alerted from `ingest_runs`.
- Zero Tier 2 rows reachable through any bulk or export endpoint.

**Kill criterion**
If days-with-≥3-entries in week 4 is below half of week 1, stop building features and re-interview the user.

---

**Build order**
1. API + data model + curated dish DB (300 entries) + text resolver + day view in bot.
2. Ingestion framework plus the Open Food Facts and Сільпо adapters, running on the scheduler.
3. Recents and repeat-yesterday.
4. Voice.
5. Mini App shell + barcode + portion screen.
6. Label OCR.

Ship 1–3 before writing any of 4–6. If text logging with a good DB doesn't hold her for three weeks, the remaining input modes won't either.

Step 1 runs against a local Postgres and a hand-seeded table. Do not write the compose file before the resolver works; the scheduled jobs exist to keep a working database fresh, not to produce one.
