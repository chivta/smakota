-- The identity provider's stable user id (the OIDC "sub" claim). Nullable:
-- users created by register, login or the CLI have none.
alter table users add column subject text;

create unique index users_subject_idx on users (subject);
