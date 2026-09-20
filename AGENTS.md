# calbot

Ukrainian calorie-logging bot. `prompt.md` is the PRD and the authority on scope;
read it before adding anything it does not list.

Backend is an HTTP API with no Telegram-specific logic. The bot and the Mini App
are clients. Go, Postgres, Redis, telebot v4, Gin, zerolog, pgx, tern.

## Rules this repo will not survive without

**`license_class` is load-bearing.** Tier 2 sources (Сільпо, Zakaz.ua, Varus,
producer and restaurant sites) forbid reproduction in their terms. Rows carrying
`lookup_only` may be rendered to one user one at a time; they must never appear
in a list endpoint, an export, or a dump. `domain.LicenseClass.Exportable()` is
the gate. Any new bulk read path calls it.

**Adapters never write to `foods`.** The pipeline is Fetch → Normalize →
Validate → Stage → Promote. Adapters stop at `staging_items`. Promote is the
only writer of catalogue rows, and it refuses to lower an entry's confidence:
hand-verified beats reference tables beats open databases beats scraped.

**Nutrition is snapshotted into `log_entries` at save time.** A later catalogue
correction must not rewrite a day the user already logged.

**Every entry is per 100 g.** Adapters convert kJ, comma decimals and ranges
before staging, or reject the row with a reason.

**Postgres, not SQLite.** The `telegram-bot` skill mandates SQLite and names its
own exception: concurrent writers. The API, the scheduler and the ingest workers
all write here, so the exception applies.

## Source research

`.claude/nutrition-sources/` holds the verified source catalogue. `sources.json`
has per-source access notes including the API quirks (Сільпо needs the numeric
`externalProductId`; Varus's real sitemap is not the one in robots.txt).
`deadends.json` lists ~50 sources already rejected with reasons. Read it before
writing any new adapter.

`research/products.txt` is the user's real diary, ordered by frequency. It is the
seeding priority list.

## Layout

    cmd/calbot          subcommands: api, scheduler, ingest <name>, seed
    internal/domain     entities, sentinel errors, the confidence ladder
    internal/config     every setting, validated at startup
    internal/storage    pgxpool, per-entity repos, embedded migrations
    internal/resolver   text parsing, candidate ranking
    internal/ingest     pipeline framework and one package per source
    internal/api        Gin handlers
    internal/telegram   telebot client
    data/dishes         the curated catalogue seed

## Conventions

Go style, error handling, wiring and logging follow the `golang-conventions`
skill. Telegram specifics follow `telegram-bot`. Neither is restated here.

User-facing Ukrainian strings live in one translation map keyed by error code.
Handlers and components contain no hard-coded text.
