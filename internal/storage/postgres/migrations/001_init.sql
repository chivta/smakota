-- Catalogue, users, logging and the ingest pipeline.

create extension if not exists pg_trgm;
create extension if not exists unaccent;

create table users (
    id                     bigserial primary key,
    telegram_id            bigint      not null unique,
    sex                    text,
    age                    int,
    height_cm              int,
    weight_kg              numeric(5,2),
    goal_weight_kg         numeric(5,2),
    activity_level         text,
    kcal_target            int,
    protein_target_g       int,
    timezone               text        not null default 'Europe/Kyiv',
    nudge_at               time        not null default '20:00',
    nudge_enabled          boolean     not null default true,
    weekly_summary_enabled boolean     not null default true,
    onboarded_at           timestamptz,
    created_at             timestamptz not null default now(),
    updated_at             timestamptz not null default now()
);

-- One catalogue entry. license_class decides what may leave the host:
-- only 'redistributable' rows may appear in a bulk response or a dump.
create table foods (
    id            bigserial primary key,
    kind          text        not null check (kind in ('product','dish')),
    name          text        not null,
    brand         text        not null default '',
    barcode       text,
    state         text        not null default 'as_sold' check (state in ('raw','cooked','as_sold')),
    kcal_100g     numeric(7,2) not null check (kcal_100g >= 0 and kcal_100g <= 900),
    protein_100g  numeric(6,2) not null check (protein_100g >= 0),
    fat_100g      numeric(6,2) not null check (fat_100g >= 0),
    carbs_100g    numeric(6,2) not null check (carbs_100g >= 0),
    package_g     numeric(8,2),
    source_id     text        not null,
    source_url    text        not null default '',
    external_id   text,
    license_class text        not null check (license_class in ('redistributable','lookup_only','user')),
    confidence    smallint    not null check (confidence between 0 and 100),
    owner_user_id bigint      references users(id) on delete cascade,
    content_hash  text,
    fetched_at    timestamptz,
    created_at    timestamptz not null default now(),
    updated_at    timestamptz not null default now()
);

create unique index foods_source_external_uniq on foods (source_id, external_id)
    where external_id is not null;
create index foods_barcode_idx on foods (barcode) where barcode is not null;
create index foods_owner_idx   on foods (owner_user_id) where owner_user_id is not null;
create index foods_name_trgm   on foods using gin (lower(name) gin_trgm_ops);

-- Ukrainian and Russian spellings, common misspellings, and the errors voice
-- transcription makes. norm is the accent-folded lowercase form the resolver
-- actually matches against.
create table food_aliases (
    id      bigserial primary key,
    food_id bigint not null references foods(id) on delete cascade,
    alias   text   not null,
    norm    text   not null,
    lang    text   not null default 'uk'
);

create index food_aliases_norm_trgm on food_aliases using gin (norm gin_trgm_ops);
create unique index food_aliases_uniq on food_aliases (food_id, norm);

-- Every dish carries at least one named portion with grams.
create table food_portions (
    id         bigserial primary key,
    food_id    bigint  not null references foods(id) on delete cascade,
    name       text    not null,
    grams      numeric(8,2) not null check (grams > 0),
    is_default boolean not null default false
);

create unique index food_portions_uniq on food_portions (food_id, name);

-- Nutrition is snapshotted at save time so a later catalogue correction never
-- rewrites a past day.
create table log_entries (
    id        bigserial primary key,
    user_id   bigint       not null references users(id) on delete cascade,
    food_id   bigint       references foods(id) on delete set null,
    food_name text         not null,
    grams     numeric(8,2) not null check (grams > 0),
    kcal      numeric(8,2) not null,
    protein   numeric(7,2) not null,
    fat       numeric(7,2) not null,
    carbs     numeric(7,2) not null,
    logged_at timestamptz  not null default now(),
    day       date         not null,
    created_at timestamptz not null default now()
);

create index log_entries_user_day_idx on log_entries (user_id, day desc);
create index log_entries_recents_idx  on log_entries (user_id, food_id, logged_at desc);

create table weights (
    id          bigserial primary key,
    user_id     bigint       not null references users(id) on delete cascade,
    weight_kg   numeric(5,2) not null check (weight_kg > 0),
    measured_on date         not null,
    created_at  timestamptz  not null default now()
);

create unique index weights_user_day_uniq on weights (user_id, measured_on);

-- Drives catalogue expansion. Every failed or refined query lands here.
create table review_queue (
    id         bigserial primary key,
    user_id    bigint references users(id) on delete set null,
    raw_input  text not null,
    input_type text not null,
    reason     text not null,
    payload    jsonb,
    resolved   boolean not null default false,
    created_at timestamptz not null default now()
);

create index review_queue_open_idx on review_queue (created_at desc) where not resolved;

-- Backs the acceptance-rate and latency criteria.
create table input_attempts (
    id                       bigserial primary key,
    user_id                  bigint references users(id) on delete cascade,
    input_type               text    not null,
    latency_ms               int     not null,
    first_candidate_accepted boolean not null default false,
    refinement_used          boolean not null default false,
    resolved                 boolean not null default false,
    food_id                  bigint references foods(id) on delete set null,
    created_at               timestamptz not null default now()
);

create index input_attempts_created_idx on input_attempts (created_at desc);

-- One row per ingest run. This table is the answer to "is the data fresh".
create table ingest_runs (
    id          bigserial primary key,
    source      text        not null,
    started_at  timestamptz not null default now(),
    finished_at timestamptz,
    fetched     int not null default 0,
    staged      int not null default 0,
    rejected    int not null default 0,
    promoted    int not null default 0,
    error       text
);

create index ingest_runs_source_idx on ingest_runs (source, started_at desc);

-- Adapters write here, never to foods. Promote diffs staged rows onto live ones.
create table staging_items (
    id            bigserial primary key,
    source_id     text not null,
    external_id   text not null,
    content_hash  text not null,
    payload       jsonb not null,
    normalized    jsonb,
    status        text not null default 'staged'
        check (status in ('staged','promoted','rejected','conflict')),
    reject_reason text,
    run_id        bigint references ingest_runs(id) on delete set null,
    created_at    timestamptz not null default now()
);

create unique index staging_items_uniq on staging_items (source_id, external_id);
create index staging_items_open_idx on staging_items (status) where status = 'staged';

-- Per-source crawl cursor, so a killed job resumes instead of restarting.
create table ingest_cursors (
    source     text primary key,
    cursor     text not null,
    updated_at timestamptz not null default now()
);
