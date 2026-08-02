CREATE TABLE IF NOT EXISTS user_privacy_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  marketing_emails BOOLEAN NOT NULL DEFAULT true,
  order_updates BOOLEAN NOT NULL DEFAULT true,
  security_alerts BOOLEAN NOT NULL DEFAULT true,
  profile_visibility TEXT NOT NULL DEFAULT 'private' CHECK (profile_visibility IN ('public', 'friends', 'private')),
  data_processing BOOLEAN NOT NULL DEFAULT true,
  analytics_tracking BOOLEAN NOT NULL DEFAULT true,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
