-- +goose Up
-- Core ledger schema (plan §4). Tenant tables live in schema `app`, which is
-- NOT in Supabase's exposed Data API schemas (core principle 4); the browser
-- never reads them directly.

CREATE SCHEMA IF NOT EXISTS app;
REVOKE ALL ON SCHEMA app FROM PUBLIC;

-- Runtime role used by the Go service. Not a superuser and without BYPASSRLS,
-- so row-level security applies to it. LOGIN/password are set out-of-band
-- (vaultctl migrate --set-app-password) so no secret lives in migrations.
-- +goose StatementBegin
DO $$ BEGIN
  CREATE ROLE vaultops_app NOLOGIN NOINHERIT;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA app TO vaultops_app;

CREATE TABLE app.organizations (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name               text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  -- sha256 of the per-tenant webhook path token; the token itself is shown once.
  webhook_token_hash bytea NOT NULL UNIQUE CHECK (length(webhook_token_hash) = 32),
  created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app.wallets (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id  uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  provider         text NOT NULL DEFAULT 'bitgo' CHECK (provider = 'bitgo'),
  provider_wallet_id text NOT NULL,
  coin             text NOT NULL,
  label            text NOT NULL DEFAULT '',
  wallet_type      text NOT NULL DEFAULT '',
  balance          numeric(78,0),
  confirmed_balance numeric(78,0),
  spendable_balance numeric(78,0),
  last_synced_at   timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id, provider, provider_wallet_id)
);

-- Ledger state is owned by the Go domain service (core principle 1):
--   pending   BitGo unconfirmed / signed / pendingApproval / initialized
--   confirmed BitGo confirmed
--   failed    BitGo failed / rejected / removed
CREATE TABLE app.transfers (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id      uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  wallet_id            uuid NOT NULL REFERENCES app.wallets(id) ON DELETE CASCADE,
  provider             text NOT NULL DEFAULT 'bitgo',
  provider_transfer_id text NOT NULL,
  coin                 text NOT NULL,
  txid                 text NOT NULL DEFAULT '',
  direction            text NOT NULL CHECK (direction IN ('send', 'receive')),
  -- signed base units, exactly as BitGo reports (negative for sends); never float
  amount               numeric(78,0) NOT NULL,
  state                text NOT NULL CHECK (state IN ('pending', 'confirmed', 'failed')),
  provider_state       text NOT NULL,
  confirmations        bigint NOT NULL DEFAULT 0,
  block_height         bigint,
  occurred_at          timestamptz NOT NULL,
  first_seen_at        timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  version              integer NOT NULL DEFAULT 1,
  UNIQUE (provider, wallet_id, provider_transfer_id)
);
CREATE INDEX transfers_org_occurred ON app.transfers (organization_id, occurred_at DESC);

CREATE TABLE app.webhook_events (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id    uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  provider           text NOT NULL DEFAULT 'bitgo',
  -- sha256 of the raw body: identical redeliveries collapse, new states don't
  provider_event_key text NOT NULL,
  status             text NOT NULL DEFAULT 'received'
                     CHECK (status IN ('received', 'processed', 'ignored', 'rejected', 'failed')),
  attempts           integer NOT NULL DEFAULT 0,
  last_error         text,
  body               jsonb NOT NULL,
  received_at        timestamptz NOT NULL,
  processed_at       timestamptz,
  UNIQUE (organization_id, provider_event_key)
);

CREATE TABLE app.security_alerts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  kind            text NOT NULL,
  severity        text NOT NULL CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
  message         text NOT NULL,
  data            jsonb NOT NULL DEFAULT '{}',
  -- optional key so the same condition raises one open alert, not many
  dedupe_key      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  resolved_at     timestamptz
);
CREATE UNIQUE INDEX security_alerts_open_dedupe
  ON app.security_alerts (organization_id, dedupe_key) WHERE resolved_at IS NULL AND dedupe_key IS NOT NULL;

CREATE TABLE app.audit_logs (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE RESTRICT,
  occurred_at     timestamptz NOT NULL DEFAULT now(),
  actor           text NOT NULL,
  action          text NOT NULL,
  entity_type     text NOT NULL,
  entity_id       text NOT NULL,
  data            jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_logs_org ON app.audit_logs (organization_id, id);

-- Narrow cross-tenant entry points (run before an org context exists).
-- +goose StatementBegin
CREATE FUNCTION app.organization_by_webhook_token(p_hash bytea)
RETURNS TABLE (id uuid, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT o.id, o.name FROM app.organizations o WHERE o.webhook_token_hash = p_hash
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION app.create_organization(p_name text, p_hash bytea)
RETURNS TABLE (id uuid, name text)
LANGUAGE sql SECURITY DEFINER SET search_path = '' AS $$
  INSERT INTO app.organizations (name, webhook_token_hash) VALUES (p_name, p_hash)
  RETURNING organizations.id, organizations.name
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.organization_by_webhook_token(bytea), app.create_organization(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.organization_by_webhook_token(bytea), app.create_organization(text, bytea) TO vaultops_app;

GRANT SELECT, INSERT, UPDATE ON app.organizations, app.wallets, app.transfers,
  app.webhook_events, app.security_alerts TO vaultops_app;
GRANT SELECT, INSERT ON app.audit_logs TO vaultops_app;

-- +goose Down
DROP SCHEMA app CASCADE;
