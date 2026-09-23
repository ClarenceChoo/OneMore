CREATE TABLE IF NOT EXISTS discovery_preferences (
  user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  categories text[] NOT NULL DEFAULT '{}',
  area text NOT NULL DEFAULT '',
  availability text NOT NULL DEFAULT 'any' CHECK (availability IN ('any','weekday_evenings','weekends')),
  timezone text NOT NULL DEFAULT 'UTC',
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS discovery_events (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  invitation_id uuid NOT NULL REFERENCES invitations(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('impression','click','join')),
  source text NOT NULL CHECK (source IN ('soonest','for_you','direct')),
  count integer NOT NULL DEFAULT 1,
  first_at timestamptz NOT NULL DEFAULT now(),
  last_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(user_id,invitation_id,kind,source)
);
CREATE INDEX IF NOT EXISTS discovery_events_user_kind_idx ON discovery_events(user_id,kind,last_at DESC);
