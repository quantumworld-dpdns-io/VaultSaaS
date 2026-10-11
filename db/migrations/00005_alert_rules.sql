-- +goose Up
-- M5: security alert rules (plan §6 M5): large transfers, new destination
-- addresses, wallet policy changes, dead-letter backlog, token expiry.

-- First outgoing counterparty of a send (for the new-destination rule).
ALTER TABLE app.transfers ADD COLUMN destination_address text NOT NULL DEFAULT '';

-- Hash of the wallet's BitGo policy as last seen; a change raises an alert.
ALTER TABLE app.wallets ADD COLUMN policy_hash bytea;

-- Admin-entered BitGo token expiry (BitGo shows it when the token is created).
ALTER TABLE app.bitgo_connections ADD COLUMN token_expires_at timestamptz;

CREATE TABLE app.known_destinations (
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  coin            text NOT NULL,
  address         text NOT NULL,
  first_seen_at   timestamptz NOT NULL DEFAULT now(),
  first_transfer  uuid REFERENCES app.transfers(id),
  PRIMARY KEY (organization_id, coin, address)
);

CREATE TABLE app.alert_settings (
  organization_id  uuid PRIMARY KEY REFERENCES app.organizations(id) ON DELETE CASCADE,
  -- {"tbtc4": "100000000", "hteth": "5000000000000000000"} — base units per coin
  large_transfer   jsonb NOT NULL DEFAULT '{}',
  dlq_threshold    integer NOT NULL DEFAULT 3 CHECK (dlq_threshold BETWEEN 1 AND 1000),
  token_warn_days  integer NOT NULL DEFAULT 14 CHECK (token_warn_days BETWEEN 1 AND 90),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE app.known_destinations ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.alert_settings     ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON app.known_destinations FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.alert_settings FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
GRANT SELECT, INSERT ON app.known_destinations TO vaultops_app;
GRANT SELECT, INSERT, UPDATE ON app.alert_settings TO vaultops_app;

-- +goose Down
DROP TABLE IF EXISTS app.alert_settings, app.known_destinations;
ALTER TABLE app.bitgo_connections DROP COLUMN IF EXISTS token_expires_at;
ALTER TABLE app.wallets DROP COLUMN IF EXISTS policy_hash;
ALTER TABLE app.transfers DROP COLUMN IF EXISTS destination_address;
