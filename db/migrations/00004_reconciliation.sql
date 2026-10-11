-- +goose Up
-- M4: expected payments (the customer's internal records), reconciliation
-- runs and classified items (plan §4, §5.2).

-- Address that received funds (for matching expected payments).
ALTER TABLE app.transfers ADD COLUMN receive_address text NOT NULL DEFAULT '';
CREATE INDEX transfers_match ON app.transfers (organization_id, receive_address, coin) WHERE direction = 'receive';

CREATE TABLE app.expected_payments (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  reference           text NOT NULL CHECK (length(reference) BETWEEN 1 AND 200),
  coin                text NOT NULL,
  address             text NOT NULL CHECK (length(address) BETWEEN 1 AND 200),
  amount              numeric(78,0) NOT NULL CHECK (amount > 0),
  expected_by         timestamptz,
  status              text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'matched', 'mismatched')),
  matched_transfer_id uuid REFERENCES app.transfers(id),
  created_by          uuid REFERENCES auth.users(id),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id, reference)
);
-- A transfer settles at most one expected payment.
CREATE UNIQUE INDEX expected_payments_one_transfer ON app.expected_payments (matched_transfer_id) WHERE matched_transfer_id IS NOT NULL;

CREATE TABLE app.reconciliation_runs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  trigger         text NOT NULL CHECK (trigger IN ('schedule', 'manual')),
  status          text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
  window_start    timestamptz NOT NULL,
  started_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz,
  stats           jsonb NOT NULL DEFAULT '{}',
  error           text
);
CREATE INDEX reconciliation_runs_org ON app.reconciliation_runs (organization_id, started_at DESC);

-- Kinds:
--   matched           expected payment settled by a receive of the same amount
--   amount_mismatch   receive at the expected address with a different amount
--   missing_bitgo     expected payment overdue; nothing received at BitGo
--   missing_internal  receive at BitGo that no expected payment accounts for ("extra inbound")
--   status_mismatch   ledger state differed from BitGo (e.g. a missed confirmation webhook); corrected
--   missed_webhook    BitGo transfer absent from the ledger (webhook never arrived); backfilled
CREATE TABLE app.reconciliation_items (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id     uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  run_id              uuid NOT NULL REFERENCES app.reconciliation_runs(id) ON DELETE CASCADE,
  kind                text NOT NULL CHECK (kind IN ('matched', 'amount_mismatch', 'missing_bitgo',
                        'missing_internal', 'status_mismatch', 'missed_webhook')),
  transfer_id         uuid REFERENCES app.transfers(id),
  expected_payment_id uuid REFERENCES app.expected_payments(id),
  details             jsonb NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reconciliation_items_run ON app.reconciliation_items (run_id);

ALTER TABLE app.expected_payments    ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.reconciliation_runs  ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.reconciliation_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON app.expected_payments FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.reconciliation_runs FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.reconciliation_items FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
GRANT SELECT, INSERT, UPDATE ON app.expected_payments, app.reconciliation_runs TO vaultops_app;
GRANT SELECT, INSERT ON app.reconciliation_items TO vaultops_app;

-- Scheduled jobs iterate over tenants that have a BitGo connection.
-- +goose StatementBegin
CREATE FUNCTION app.organizations_with_bitgo() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT c.organization_id FROM app.bitgo_connections c ORDER BY 1
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION app.all_organizations() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT o.id FROM app.organizations o ORDER BY 1
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.organizations_with_bitgo(), app.all_organizations() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.organizations_with_bitgo(), app.all_organizations() TO vaultops_app;

-- +goose Down
DROP FUNCTION IF EXISTS app.all_organizations(), app.organizations_with_bitgo();
DROP TABLE IF EXISTS app.reconciliation_items, app.reconciliation_runs, app.expected_payments;
DROP INDEX IF EXISTS app.transfers_match;
ALTER TABLE app.transfers DROP COLUMN IF EXISTS receive_address;
