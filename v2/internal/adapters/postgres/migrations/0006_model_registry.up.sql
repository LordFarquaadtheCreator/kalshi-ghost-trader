-- Model registry with promotion ladder (Addendum A.4).
CREATE TABLE IF NOT EXISTS model_registry (
  id            bigserial PRIMARY KEY,
  family        text   NOT NULL,
  version       int    NOT NULL,
  trained_at    bigint NOT NULL,
  train_from_ts bigint NOT NULL,
  train_to_ts   bigint NOT NULL,
  feature_hash  text   NOT NULL,
  artifact_path text   NOT NULL,
  artifact_sha  text   NOT NULL,
  metrics       jsonb  NOT NULL,
  status        text   NOT NULL CHECK (status IN
                  ('candidate','shadow','paper','champion','retired')),
  trial_index   int    NOT NULL,
  UNIQUE (family, version)
);

CREATE INDEX IF NOT EXISTS idx_model_registry_family_status
  ON model_registry (family, status);
