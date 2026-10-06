ALTER TABLE posts
  ADD COLUMN private_verification_detail VARCHAR(300),
  ADD COLUMN freshness_confirmed_at TIMESTAMPTZ;

ALTER TABLE revisions
  ADD COLUMN private_verification_detail VARCHAR(300);

ALTER TABLE requests
  ADD COLUMN verification_answer VARCHAR(500);

CREATE INDEX posts_freshness_active
  ON posts ((COALESCE(GREATEST(freshness_confirmed_at, approved_at), approved_at, created_at)))
  WHERE moderation_status='已通过' AND withdrawn=false
    AND lifecycle_status IN ('寻找中','待认领');

CREATE INDEX posts_author_active ON posts(author_id, created_at DESC);
