-- Rejections recorded that something failed but never what. The reason column
-- holds the review category, so the adapter's own message had nowhere to go.
alter table review_queue add column detail text;
