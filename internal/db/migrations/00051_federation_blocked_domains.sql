-- +goose Up
-- Instance defederation (federation Phase 5): an operator-managed blocklist of
-- remote domains. Inbound activities signed by an actor on a blocked domain are
-- rejected (403), outbound actor fetches refuse the domain, and the delivery
-- fan-out skips inboxes there. Domains are stored lowercased (host[:port]).
CREATE TABLE federation_blocked_domains (
    domain     text        PRIMARY KEY,
    reason     text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE federation_blocked_domains;
