package storage

import "database/sql"

// A transaction-scoped store reuses the same validation/storage routines without
// accidentally committing part of a telemetry envelope outside its transaction.
type sqlExecutor interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func (s *Store) db() sqlExecutor {
	if s.executor != nil {
		return s.executor
	}
	return s.DB
}
