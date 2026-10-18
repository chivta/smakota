-- Notification settings are gone: nothing in the API sends messages.

alter table users
    drop column nudge_at,
    drop column nudge_enabled,
    drop column weekly_summary_enabled;
