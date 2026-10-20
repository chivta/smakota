-- Who contributed a catalogue row. Attribution only: nothing filters on it,
-- so a contributed row stays visible to everyone.
alter table foods add column created_by_user_id bigint references users(id) on delete set null;

-- Personal foods become shared ones: the owner is recorded as the contributor
-- and the row stops being private.
update foods
set created_by_user_id = owner_user_id, owner_user_id = null
where owner_user_id is not null and license_class = 'user';
