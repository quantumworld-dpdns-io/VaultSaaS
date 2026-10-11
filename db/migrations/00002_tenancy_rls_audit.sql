-- +goose Up
-- M3: membership, invitations, per-org BitGo connections, linked wallets,
-- row-level security on every app table (core principle 3) and a
-- tamper-evident, append-only audit log (plan §4).

-- ---- session context helpers ------------------------------------------------
-- +goose StatementBegin
CREATE FUNCTION app.current_org() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.org_id', true), '')::uuid
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION app.current_user_id() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION app.current_org(), app.current_user_id() TO vaultops_app;

-- ---- membership -------------------------------------------------------------
CREATE TABLE app.organization_members (
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  role            text NOT NULL CHECK (role IN ('owner', 'admin', 'approver', 'viewer')),
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, user_id)
);

CREATE TABLE app.invitations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid NOT NULL REFERENCES app.organizations(id) ON DELETE CASCADE,
  email           text NOT NULL CHECK (email = lower(email)),
  role            text NOT NULL CHECK (role IN ('admin', 'approver', 'viewer')),
  token_hash      bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  invited_by      uuid NOT NULL REFERENCES auth.users(id),
  expires_at      timestamptz NOT NULL,
  accepted_at     timestamptz,
  accepted_by     uuid REFERENCES auth.users(id),
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- ---- BitGo connections (token encrypted by the app, AES-256-GCM) ------------
CREATE TABLE app.bitgo_connections (
  organization_id uuid PRIMARY KEY REFERENCES app.organizations(id) ON DELETE CASCADE,
  environment     text NOT NULL DEFAULT 'test' CHECK (environment IN ('test', 'prod')),
  enterprise_id   text NOT NULL DEFAULT '',
  encrypted_token bytea NOT NULL,
  key_version     integer NOT NULL,
  owner_user_id   uuid NOT NULL REFERENCES auth.users(id),
  verified_at     timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

-- ---- linked wallets (D4: a linked identity, never an account root) ----------
CREATE TABLE app.user_wallets (
  user_id     uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  chain       text NOT NULL CHECK (chain = 'eip155'),
  address     text NOT NULL CHECK (address ~ '^0x[0-9a-f]{40}$'),
  verified_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (chain, address)
);
CREATE INDEX user_wallets_user ON app.user_wallets (user_id);

CREATE TABLE app.siwe_nonces (
  nonce      text PRIMARY KEY CHECK (length(nonce) >= 16),
  user_id    uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);

-- ---- row-level security -----------------------------------------------------
ALTER TABLE app.organizations        ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.organization_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.invitations          ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.bitgo_connections    ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.wallets              ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.transfers            ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.webhook_events       ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.security_alerts      ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.audit_logs           ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.user_wallets         ENABLE ROW LEVEL SECURITY;
ALTER TABLE app.siwe_nonces          ENABLE ROW LEVEL SECURITY;

CREATE POLICY org_self ON app.organizations FOR ALL TO vaultops_app
  USING (id = app.current_org()) WITH CHECK (id = app.current_org());

CREATE POLICY tenant ON app.organization_members FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.invitations FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.bitgo_connections FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.wallets FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.transfers FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.webhook_events FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
CREATE POLICY tenant ON app.security_alerts FOR ALL TO vaultops_app
  USING (organization_id = app.current_org()) WITH CHECK (organization_id = app.current_org());
-- audit: read + append only; there is deliberately no UPDATE/DELETE policy
CREATE POLICY tenant_read ON app.audit_logs FOR SELECT TO vaultops_app
  USING (organization_id = app.current_org());
CREATE POLICY tenant_append ON app.audit_logs FOR INSERT TO vaultops_app
  WITH CHECK (organization_id = app.current_org());

CREATE POLICY own ON app.user_wallets FOR ALL TO vaultops_app
  USING (user_id = app.current_user_id()) WITH CHECK (user_id = app.current_user_id());
CREATE POLICY own ON app.siwe_nonces FOR ALL TO vaultops_app
  USING (user_id = app.current_user_id()) WITH CHECK (user_id = app.current_user_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON app.organization_members TO vaultops_app;
GRANT SELECT, INSERT, UPDATE ON app.invitations, app.siwe_nonces TO vaultops_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON app.bitgo_connections TO vaultops_app;
GRANT SELECT, INSERT, DELETE ON app.user_wallets TO vaultops_app;

-- ---- narrow cross-tenant entry points (SECURITY DEFINER) --------------------

-- Role of a user in an org, or NULL. Used for authorization before the org
-- context is set.
-- +goose StatementBegin
CREATE FUNCTION app.member_role(p_org uuid, p_user uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT m.role FROM app.organization_members m WHERE m.organization_id = p_org AND m.user_id = p_user
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.my_organizations(p_user uuid)
RETURNS TABLE (id uuid, name text, role text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT o.id, o.name, m.role FROM app.organization_members m
  JOIN app.organizations o ON o.id = m.organization_id
  WHERE m.user_id = p_user ORDER BY o.name
$$;
-- +goose StatementEnd

-- Creates an organization with its first owner in one step.
-- +goose StatementBegin
CREATE FUNCTION app.create_organization_with_owner(p_name text, p_hash bytea, p_owner uuid)
RETURNS TABLE (id uuid, name text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE o_id uuid;
BEGIN
  INSERT INTO app.organizations (name, webhook_token_hash) VALUES (p_name, p_hash) RETURNING organizations.id INTO o_id;
  INSERT INTO app.organization_members (organization_id, user_id, role) VALUES (o_id, p_owner, 'owner');
  RETURN QUERY SELECT o_id, p_name;
END $$;
-- +goose StatementEnd

-- Accepts an invitation addressed to the caller's email.
-- +goose StatementBegin
CREATE FUNCTION app.accept_invitation(p_hash bytea, p_user uuid, p_email text)
RETURNS TABLE (organization_id uuid, role text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE inv app.invitations%ROWTYPE;
BEGIN
  SELECT * INTO inv FROM app.invitations i WHERE i.token_hash = p_hash FOR UPDATE;
  IF NOT FOUND OR inv.accepted_at IS NOT NULL OR inv.expires_at < now() THEN
    RAISE EXCEPTION 'invitation invalid or expired' USING ERRCODE = 'P0002';
  END IF;
  IF inv.email <> lower(p_email) THEN
    RAISE EXCEPTION 'invitation is for a different email' USING ERRCODE = '42501';
  END IF;
  INSERT INTO app.organization_members (organization_id, user_id, role)
  VALUES (inv.organization_id, p_user, inv.role)
  ON CONFLICT ON CONSTRAINT organization_members_pkey DO NOTHING;
  UPDATE app.invitations SET accepted_at = now(), accepted_by = p_user WHERE id = inv.id;
  RETURN QUERY SELECT inv.organization_id, inv.role;
END $$;
-- +goose StatementEnd

-- Owner of a linked wallet address, if any (to refuse linking it twice).
-- +goose StatementBegin
CREATE FUNCTION app.wallet_owner(p_chain text, p_address text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT w.user_id FROM app.user_wallets w WHERE w.chain = p_chain AND w.address = p_address
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION app.member_role(uuid, uuid), app.my_organizations(uuid),
  app.create_organization_with_owner(text, bytea, uuid), app.accept_invitation(bytea, uuid, text),
  app.wallet_owner(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.member_role(uuid, uuid), app.my_organizations(uuid),
  app.create_organization_with_owner(text, bytea, uuid), app.accept_invitation(bytea, uuid, text),
  app.wallet_owner(text, text) TO vaultops_app;

-- ---- tamper-evident audit log ------------------------------------------------
ALTER TABLE app.audit_logs ADD COLUMN prev_hash bytea, ADD COLUMN hash bytea;

-- +goose StatementBegin
CREATE FUNCTION app.audit_row_digest(r app.audit_logs, prev bytea) RETURNS bytea
LANGUAGE sql IMMUTABLE SET search_path = '' AS $$
  SELECT sha256(coalesce(prev, '\x'::bytea) || convert_to(concat_ws('|',
    r.organization_id::text, r.id::text,
    to_char(r.occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    r.actor, r.action, r.entity_type, r.entity_id, r.data::text), 'UTF8'))
$$;
-- +goose StatementEnd

-- Chains each row to the previous row of the same organization. The advisory
-- lock serialises appends per org so the chain cannot fork.
-- +goose StatementBegin
CREATE FUNCTION app.audit_chain() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('audit:' || NEW.organization_id::text, 0));
  SELECT a.hash INTO NEW.prev_hash FROM app.audit_logs a
   WHERE a.organization_id = NEW.organization_id ORDER BY a.id DESC LIMIT 1;
  NEW.hash := app.audit_row_digest(NEW, NEW.prev_hash);
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app.audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit_logs is append-only (% rejected)', TG_OP USING ERRCODE = '42501';
END $$;
-- +goose StatementEnd

CREATE TRIGGER audit_chain BEFORE INSERT ON app.audit_logs FOR EACH ROW EXECUTE FUNCTION app.audit_chain();
CREATE TRIGGER audit_no_update BEFORE UPDATE OR DELETE ON app.audit_logs FOR EACH ROW EXECUTE FUNCTION app.audit_append_only();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON app.audit_logs FOR EACH STATEMENT EXECUTE FUNCTION app.audit_append_only();

-- Backfill hashes for rows written before this migration (M2), in order.
-- +goose StatementBegin
DO $$
DECLARE r app.audit_logs%ROWTYPE; prev bytea; cur_org uuid;
BEGIN
  ALTER TABLE app.audit_logs DISABLE TRIGGER audit_no_update;
  FOR r IN SELECT * FROM app.audit_logs ORDER BY organization_id, id LOOP
    IF cur_org IS DISTINCT FROM r.organization_id THEN prev := NULL; cur_org := r.organization_id; END IF;
    UPDATE app.audit_logs SET prev_hash = prev, hash = app.audit_row_digest(r, prev) WHERE id = r.id;
    prev := app.audit_row_digest(r, prev);
  END LOOP;
  ALTER TABLE app.audit_logs ENABLE TRIGGER audit_no_update;
END $$;
-- +goose StatementEnd
ALTER TABLE app.audit_logs ALTER COLUMN hash SET NOT NULL;

-- First row whose hash does not verify, or NULL when the chain is intact.
-- +goose StatementBegin
CREATE FUNCTION app.verify_audit_chain(p_org uuid) RETURNS bigint
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = '' AS $$
DECLARE r app.audit_logs%ROWTYPE; prev bytea;
BEGIN
  FOR r IN SELECT * FROM app.audit_logs a WHERE a.organization_id = p_org ORDER BY a.id LOOP
    IF r.prev_hash IS DISTINCT FROM prev OR r.hash <> app.audit_row_digest(r, prev) THEN
      RETURN r.id;
    END IF;
    prev := r.hash;
  END LOOP;
  RETURN NULL;
END $$;
-- +goose StatementEnd

-- Head of the chain per org, for the daily off-database anchor (R2 bucket lock).
-- +goose StatementBegin
CREATE FUNCTION app.audit_heads() RETURNS TABLE (organization_id uuid, last_id bigint, head_hash bytea, entries bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT DISTINCT ON (a.organization_id) a.organization_id, a.id, a.hash,
         count(*) OVER (PARTITION BY a.organization_id)
  FROM app.audit_logs a ORDER BY a.organization_id, a.id DESC
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.verify_audit_chain(uuid), app.audit_heads() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.verify_audit_chain(uuid), app.audit_heads() TO vaultops_app;

-- +goose Down
DROP FUNCTION IF EXISTS app.audit_heads(), app.verify_audit_chain(uuid);
DROP TRIGGER IF EXISTS audit_no_truncate ON app.audit_logs;
DROP TRIGGER IF EXISTS audit_no_update ON app.audit_logs;
DROP TRIGGER IF EXISTS audit_chain ON app.audit_logs;
DROP FUNCTION IF EXISTS app.audit_append_only(), app.audit_chain(), app.audit_row_digest(app.audit_logs, bytea);
ALTER TABLE app.audit_logs DROP COLUMN IF EXISTS hash, DROP COLUMN IF EXISTS prev_hash;
DROP FUNCTION IF EXISTS app.wallet_owner(text, text), app.accept_invitation(bytea, uuid, text),
  app.create_organization_with_owner(text, bytea, uuid), app.my_organizations(uuid), app.member_role(uuid, uuid);
DROP TABLE IF EXISTS app.siwe_nonces, app.user_wallets, app.bitgo_connections, app.invitations, app.organization_members;
ALTER TABLE app.organizations DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS org_self ON app.organizations;
DROP FUNCTION IF EXISTS app.current_user_id(), app.current_org();
