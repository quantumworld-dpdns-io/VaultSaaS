-- +goose Up
-- Members of the CURRENT org with their emails. auth.users stays unreadable
-- to the runtime role; this exposes only the email of fellow members.
-- +goose StatementBegin
CREATE FUNCTION app.current_org_members()
RETURNS TABLE (user_id uuid, email text, role text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = '' AS $$
  SELECT m.user_id, coalesce(u.email::text, ''), m.role, m.created_at
  FROM app.organization_members m
  LEFT JOIN auth.users u ON u.id = m.user_id
  WHERE m.organization_id = app.current_org()
  ORDER BY m.created_at
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.current_org_members() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.current_org_members() TO vaultops_app;

-- +goose Down
DROP FUNCTION IF EXISTS app.current_org_members();
