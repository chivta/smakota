-- Per-user bearer tokens replace the shared service token and Telegram initData.
-- telegram_id stays as an optional linked identity; existing values are kept.

alter table users alter column telegram_id drop not null;
alter table users drop constraint users_telegram_id_key;

create table api_tokens (
    id           bigserial primary key,
    user_id      bigint      not null references users (id) on delete cascade,
    name         text        not null,
    token_hash   bytea       not null,
    created_at   timestamptz not null default now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

create unique index api_tokens_token_hash_idx on api_tokens (token_hash);
create index api_tokens_user_idx on api_tokens (user_id);
