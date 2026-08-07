// Package sqldb opens the one SQLite database the app uses.
//
// It exists because more than one domain now persists into the same file, and a
// second *sql.DB on the same SQLite database would put two independent pools
// behind a store that assumes a single writer. Opening here once and handing the
// handle to each store keeps that assumption true no matter how many domains are
// added later.
package sqldb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Open opens (creating if needed) the database at path.
//
// WAL keeps a reader from blocking the writer, busy_timeout makes a contended
// write wait rather than fail with SQLITE_BUSY, and foreign_keys is on so a
// future schema that declares a reference actually gets it enforced — SQLite
// ignores them by default.
//
// The pool is capped at one connection. SQLite serialises writes anyway, and for
// a single-file app a serial pool removes lock contention as a class of bug at no
// cost worth measuring.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqldb: opening %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("sqldb: connecting to %s: %w", path, err)
	}

	return db, nil
}
