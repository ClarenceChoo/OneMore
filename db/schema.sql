CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY,
  subject text UNIQUE NOT NULL,
  name text NOT NULL,
  email text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS invitations (
  id uuid PRIMARY KEY,
  host_id uuid NOT NULL REFERENCES users(id),
  title text NOT NULL CHECK (char_length(title) BETWEEN 3 AND 80),
  description text NOT NULL DEFAULT '',
  category text NOT NULL,
  tags text[] NOT NULL DEFAULT '{}',
  area text NOT NULL,
  location text NOT NULL,
  starts_at timestamptz NOT NULL,
  join_deadline timestamptz NOT NULL,
  spaces integer NOT NULL CHECK (spaces BETWEEN 1 AND 20),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','cancelled')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS invitations_discovery_idx ON invitations(status,starts_at,id);
CREATE TABLE IF NOT EXISTS memberships (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  invitation_id uuid NOT NULL REFERENCES invitations(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id),
  status text NOT NULL CHECK (status IN ('confirmed','waitlisted','offered','left')),
  waitlist_order bigint,
  joined_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(invitation_id,user_id)
);
CREATE SEQUENCE IF NOT EXISTS waitlist_order_seq;
CREATE INDEX IF NOT EXISTS memberships_queue_idx ON memberships(invitation_id,waitlist_order) WHERE status='waitlisted';
CREATE TABLE IF NOT EXISTS offers (
  id uuid PRIMARY KEY,
  membership_id bigint NOT NULL REFERENCES memberships(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  status text NOT NULL CHECK (status IN ('active','accepted','declined','expired','cancelled')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS offers_active_idx ON offers(expires_at) WHERE status='active';
CREATE UNIQUE INDEX IF NOT EXISTS offers_one_active_per_member ON offers(membership_id) WHERE status='active';
CREATE TABLE IF NOT EXISTS notifications (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id),
  event_id uuid NOT NULL UNIQUE,
  message text NOT NULL,
  invitation_id uuid REFERENCES invitations(id),
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notifications_user_idx ON notifications(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS outbox_events (
  id uuid PRIMARY KEY,
  kind text NOT NULL,
  invitation_id uuid NOT NULL REFERENCES invitations(id),
  user_id uuid REFERENCES users(id),
  message text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_pending_idx ON outbox_events(created_at) WHERE delivered_at IS NULL;
CREATE TABLE IF NOT EXISTS idempotency_records (
  user_id uuid NOT NULL REFERENCES users(id),
  key text NOT NULL,
  invitation_id uuid NOT NULL REFERENCES invitations(id),
  PRIMARY KEY(user_id,key)
);
