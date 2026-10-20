-- Password credentials for self-service accounts. Both columns are nullable:
-- users created by the CLI have no credentials and keep using their tokens.
-- The email is stored lowercased; the index makes uniqueness case-insensitive
-- even for a row written by something that did not lowercase it.
alter table users add column email text;
alter table users add column password_hash text;

create unique index users_email_lower_idx on users (lower(email));
