package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

// Older writers used variable-width fractional seconds. Lexical order placed
// 10:00:00Z AFTER 10:00:00.5Z, corrupting point queries. Normalize old UTC values
// without changing instants, once, in a transaction. Back up before upgrading.
func (s *Store) normalizeTimes() error {
	const version = 2026091301
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.WithTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			return err
		}
		tables := []string{}
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, table)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, table := range tables {
			info, err := tx.Query(`PRAGMA table_info("` + table + `")`)
			if err != nil {
				return err
			}
			cols := []string{}
			for info.Next() {
				var cid, notnull, pk int
				var name, typ string
				var def any
				if err := info.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
					info.Close()
					return err
				}
				if strings.HasSuffix(name, "_at") || name == "deadline" || name == "since" {
					cols = append(cols, name)
				}
			}
			info.Close()
			for _, col := range cols {
				q := fmt.Sprintf(`UPDATE "%s" SET "%s"=CASE WHEN length("%s")=20 THEN substr("%s",1,19)||'.000000000Z' ELSE substr("%s",1,length("%s")-1)||substr('000000000',1,30-length("%s"))||'Z' END WHERE "%s" GLOB '????-??-??T??:??:??*Z' AND length("%s") BETWEEN 20 AND 29`, table, col, col, col, col, col, col, col, col)
				if _, err := tx.Exec(q); err != nil {
					return err
				}
			}
		}
		_, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, s.now().Format(dbTimeFormat))
		return err
	})
}
