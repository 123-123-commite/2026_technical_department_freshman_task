CREATE TABLE users (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(32) NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  nickname VARCHAR(40) NOT NULL,
  student_number VARCHAR(32) NOT NULL UNIQUE,
  role VARCHAR(16) NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE refresh_tokens (
  jti UUID PRIMARY KEY,
  family_id UUID NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);
CREATE INDEX refresh_tokens_family ON refresh_tokens(family_id);
CREATE TABLE posts (
  id BIGSERIAL PRIMARY KEY,
  author_id BIGINT NOT NULL REFERENCES users(id),
  kind VARCHAR(8) NOT NULL CHECK(kind IN ('lost','found')),
  item_name VARCHAR(100) NOT NULL,
  description VARCHAR(3000) NOT NULL,
  category VARCHAR(40), location VARCHAR(120), event_time VARCHAR(80),
  lifecycle_status VARCHAR(16) NOT NULL,
  moderation_status VARCHAR(16) NOT NULL DEFAULT '待审核',
  rejection_reason TEXT,
  withdrawn BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), approved_at TIMESTAMPTZ
);
CREATE TABLE revisions (
  post_id BIGINT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
  item_name VARCHAR(100) NOT NULL, description VARCHAR(3000) NOT NULL,
  category VARCHAR(40), location VARCHAR(120), event_time VARCHAR(80),
  moderation_status VARCHAR(16) NOT NULL DEFAULT '待审核', rejection_reason TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE requests (
  id BIGSERIAL PRIMARY KEY, post_id BIGINT NOT NULL REFERENCES posts(id),
  requester_id BIGINT NOT NULL REFERENCES users(id), kind VARCHAR(8) NOT NULL,
  explanation VARCHAR(2000) NOT NULL, contact_method VARCHAR(120) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT '待处理', resolution_reason TEXT,
  removed BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE reports (
  id BIGSERIAL PRIMARY KEY, reporter_id BIGINT NOT NULL REFERENCES users(id),
  target_type VARCHAR(8) NOT NULL CHECK(target_type IN ('post','request')), target_id BIGINT NOT NULL,
  explanation VARCHAR(2000) NOT NULL, status VARCHAR(16) NOT NULL DEFAULT '待处理',
  moderator_reason TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE audit_log (
  id BIGSERIAL PRIMARY KEY, actor_id BIGINT NOT NULL REFERENCES users(id), action TEXT NOT NULL,
  target_type TEXT NOT NULL, target_id BIGINT NOT NULL, reason TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX posts_public_order ON posts(moderation_status, withdrawn, approved_at DESC);
CREATE INDEX requests_post_status ON requests(post_id, status);
CREATE INDEX reports_status ON reports(status, created_at);
