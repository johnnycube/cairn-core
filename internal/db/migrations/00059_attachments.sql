-- Activity attachments (photos/media). Bytes live in blob storage (blob_id =
-- S3 key); this table holds the metadata. Provider-imported attachments carry
-- the source they came from so a reimport can replace exactly that set; user
-- uploads have source_id NULL.

-- +goose Up
CREATE TABLE activity_attachments (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    activity_id  uuid NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    source_id    uuid REFERENCES activity_sources(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blob_id      text NOT NULL,
    external_url text NOT NULL DEFAULT '',
    content_type text NOT NULL DEFAULT 'application/octet-stream',
    caption      text NOT NULL DEFAULT '',
    width        int NOT NULL DEFAULT 0,
    height       int NOT NULL DEFAULT 0,
    position     int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_attachments_activity_idx
    ON activity_attachments (activity_id, position, created_at);
CREATE INDEX activity_attachments_source_idx
    ON activity_attachments (source_id) WHERE source_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS activity_attachments;
