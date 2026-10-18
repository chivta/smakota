# calbot

A food logging HTTP API with a Ukrainian-first food database. `prompt.md` is
the PRD and the authority on scope; read it before adding anything it does not
list.

There are no clients in this repo. A Telegram bot lived here until it was
removed to let the API stabilise first; recover it from history if needed.
Client-shaped work does not belong here.

Go, Postgres, Gin, zerolog, pgx, tern.

## Rules this repo will not survive without

**`license_class` is load-bearing.** Tier 2 sources (Сільпо, Zakaz.ua, Varus,
producer and restaurant sites) forbid reproduction in their terms. Rows
carrying `lookup_only` may be rendered to one caller at a time; they must never
appear in an export or a dump. `domain.LicenseClass.Exportable()` is the gate,
and any new bulk read path calls it. Whether a paginated search counts as bulk
is an open decision, recorded in `.claude/api-redesign.html`

**Adapters never write to `foods`.** The pipeline is Fetch → Normalize →
Validate → Stage → Promote. Adapters stop at `staging_items`. Promote is the
only writer of catalogue rows, and it refuses to lower an entry's confidence:
hand-verified beats reference tables beats open databases beats scraped.

**Each adapter declares its own provenance.** `Adapter.Provenance()` returns the
licence class and confidence for its rows. A new source is a new package, never
an edit to a switch in the shared normalizer.

**Nutrition is snapshotted into `log_entries` at save time.** A later catalogue
correction must not rewrite a day already logged.

**Every catalogue entry is per 100 g.** Adapters convert kJ, comma decimals and
ranges before staging, or reject the row with a reason.

## Source research

`.claude/nutrition-sources/` holds the verified source catalogue. `sources.json`
has per-source access notes including the API quirks (Сільпо filters products by
category *slug*, not by the UUID id; Varus's real sitemap is not the one in
robots.txt). `deadends.json` lists ~50 sources already rejected with reasons.
Read it before writing any new adapter.

`research/products.txt` is a real diary, ordered by frequency, and the seeding
priority list.

## Layout

    cmd/calbot          subcommands: api, scheduler, ingest <name>, seed, migrate, health
    internal/domain     entities, sentinel errors, the confidence ladder
    internal/config     every setting, validated at startup
    internal/storage    pgxpool, per-entity repos, embedded migrations
    internal/resolver   query normalization, quantity parsing, candidate ranking
    internal/ingest     pipeline framework and one package per source
    internal/api        Gin handlers and the OpenAPI spec served at /docs
    internal/seed       the curated catalogue, embedded in the binary

## Two seams worth knowing

**Alias matching.** `food_aliases.norm` is written by `FoodRepo.SeedCatalogue`
and read by `FoodRepo.Search`, and both must use `resolver.Normalize`. Normalize
with anything else and every curated entry silently stops matching.

**Named-type adapters.** The ingest framework and the resolver each declare the
storage they need on their own side, so neither imports the repository package.
The field shapes agree; the named types do not. `cmd/calbot/adapters.go` is the
only place that bridges them.

## Conventions

Go style, error handling, wiring and logging follow the `golang-conventions`
skill, which is not restated here.

Errors carry a machine-readable code. Callers map codes to their own text.

The OpenAPI spec is hand-written and lives beside the handlers. Nothing
generates it, so changing a handler means changing the spec in the same commit.
