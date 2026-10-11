package store

import "github.com/jackc/pgx/v5"

// QueryRowForTest lets external tests read through an org-scoped transaction.
func (t *Tx) QueryRowForTest(sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(t.ctx, sql, args...)
}

// ExecForTest runs a statement in the session and returns rows affected.
func (t *Tx) ExecForTest(sql string, args ...any) (int64, error) {
	ct, err := t.tx.Exec(t.ctx, sql, args...)
	return ct.RowsAffected(), err
}
