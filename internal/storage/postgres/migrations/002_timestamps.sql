-- updated_at is maintained by the database; log_entries drops day in favour of
-- logged_at ranges.

create function set_updated_at() returns trigger as $$
begin
    new.updated_at = now();
    return new;
end;
$$ language plpgsql;

alter table log_entries add column updated_at timestamptz not null default now();
update log_entries set updated_at = created_at;

create trigger users_set_updated_at before update on users
    for each row execute function set_updated_at();
create trigger foods_set_updated_at before update on foods
    for each row execute function set_updated_at();
create trigger log_entries_set_updated_at before update on log_entries
    for each row execute function set_updated_at();

drop index log_entries_user_day_idx;
alter table log_entries drop column day;

create index log_entries_user_logged_idx on log_entries (user_id, logged_at desc);
