-- A bulk source rejects millions of rows, so only a capped sample per reason
-- reaches review_queue. This column says how many rows of the run were
-- counted but not stored.
alter table ingest_runs add column rejected_dropped int not null default 0;

-- Promote reads pending rows in id order. The old index was keyed on status
-- alone, which forced a sort of every pending row on each batch.
drop index staging_items_open_idx;
create index staging_items_open_idx on staging_items (source_id, id) where status = 'staged';
