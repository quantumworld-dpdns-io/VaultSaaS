package store

import "github.com/jackc/pgx/v5"

// QueryRowForTest lets external tests read through an org-scoped transaction.
func (t *Tx) QueryRowForTest(sql string, args ...any) pgx.Row { return t.tx.QueryRow(t.ctx, sql, args...) }
