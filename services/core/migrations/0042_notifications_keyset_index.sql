-- +goose Up
-- +goose StatementBegin
-- Keyset-pagination index for the BOUNDED notification feed (issue #128, §17
-- bounded reads). The feed query (ListNotificationsPage) reads an account's
-- notifications newest-first with a cursor over (created_at, id):
--
--   WHERE marketplace_account_id = $1
--     AND (created_at, id) < (cursor_created_at, cursor_id)
--   ORDER BY created_at DESC, id DESC
--   LIMIT page_limit
--
-- This composite index matches that predicate and ordering EXACTLY —
-- (marketplace_account_id, created_at DESC, id DESC) — so Postgres serves each page
-- as an index range scan seeked to the cursor position, never a full-history scan
-- that grows with the append-only feed. The pre-existing
-- idx_notifications_account_created (marketplace_account_id, created_at DESC) omits
-- the id tie-break, so it cannot resolve the (created_at, id) keyset boundary for
-- rows sharing a timestamp; this index adds the id DESC column that makes ties
-- deterministic and every row returned exactly once across pages.
--
-- Additive and read-only: it changes no data and no append-only guarantee. The
-- partial unread index (idx_notifications_account_unread) still backs the badge
-- count; this index backs the paged read.
CREATE INDEX idx_notifications_account_created_id
    ON notifications (marketplace_account_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_notifications_account_created_id;
-- +goose StatementEnd
