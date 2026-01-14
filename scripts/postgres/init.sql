CREATE TABLE IF NOT EXISTS rules (
  tenant_id text NOT NULL,
  resource text NOT NULL,
  algorithm text NOT NULL,
  "limit" bigint NOT NULL,
  window_ms bigint NOT NULL,
  burst_size bigint NOT NULL,
  version bigint NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, resource)
);

CREATE TABLE IF NOT EXISTS outbox (
  id text PRIMARY KEY,
  data bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at timestamptz NULL
);

CREATE INDEX IF NOT EXISTS outbox_created_at_idx ON outbox (created_at);
CREATE INDEX IF NOT EXISTS outbox_sent_at_idx ON outbox (sent_at);
