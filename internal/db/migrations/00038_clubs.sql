-- Clubs / groups / teams (multi-user v1). A club is a named group with a public
-- or private membership; members' activities surface in a shared club feed.

-- +goose Up
CREATE TABLE clubs (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    slug        citext NOT NULL UNIQUE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    owner_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_public   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE club_members (
    club_id   uuid NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (club_id, user_id)
);
CREATE INDEX club_members_user_idx ON club_members (user_id);

-- +goose Down
DROP TABLE club_members;
DROP TABLE clubs;
