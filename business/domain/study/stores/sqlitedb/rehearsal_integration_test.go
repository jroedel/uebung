//go:build integration

// This file is the migration rehearsal: it runs a schema change against a copy of
// a real database and asserts that real data came through it intact.
//
// It is tagged `integration` and skipped unless UEBUNG_REHEARSAL_DB names a
// database, because it needs something the repository cannot carry — a production
// backup. `make test` stays runnable on any machine; `make test-integration
// UEBUNG_REHEARSAL_DB=...` is what you run before shipping a migration.
//
// Why this exists as a test rather than as something done by hand: the unit tests
// prove the migration works on data the tests themselves wrote, which is data
// shaped exactly the way the migration expects. A real database is where the
// surprises live — a lemma nobody would think to write, a row from a build two
// versions ago, a language that was tried once. The rehearsal is the only check
// that sees those.
//
// It always works on a copy. Nothing here writes to the file you point it at.
package sqlitedb_test

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/sqldb"
)

// rehearsalEnv names the database to rehearse against. Point it at a backup —
// deploy.sh leaves them in backups/ — never at a live file.
const rehearsalEnv = "UEBUNG_REHEARSAL_DB"

// legacyCard is one pre-migration row, captured before the migration runs so it
// can be compared with what comes out the other side. It holds the raw stored
// strings rather than parsed values on purpose: the question is whether the bytes
// survived, and parsing first would hide a row that was already unparseable.
type legacyCard struct {
	user       string
	lang       string
	lemma      string
	stability  float64
	difficulty float64
	state      string
	reps       int
	lapses     int
	lastReview string
	due        string
}

// rehearsalDB copies the nominated database into the test's temp directory and
// opens the copy, so the rehearsal can never damage the thing it is rehearsing
// against. It skips the test when no database was nominated.
func rehearsalDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	src := os.Getenv(rehearsalEnv)
	if src == "" {
		t.Skipf("set %s to a database backup to run the migration rehearsal", rehearsalEnv)
	}

	if _, err := os.Stat(src); err != nil {
		t.Fatalf("%s=%q: %v", rehearsalEnv, src, err)
	}

	dst := filepath.Join(t.TempDir(), "rehearsal.db")
	copyFile(t, src, dst)

	// A backup taken by `cp` may have a sidecar WAL holding committed transactions.
	// Without it the copy silently reads as an older database, and the rehearsal
	// would pass against data that is not what production actually holds.
	if _, err := os.Stat(src + "-wal"); err == nil {
		copyFile(t, src+"-wal", dst+"-wal")
	}

	db, err := sqldb.Open(t.Context(), dst)
	if err != nil {
		t.Fatalf("opening the copy: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db, src
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()

	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("opening %s: %v", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("creating %s: %v", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}

// hasColumn reports whether a table carries a named column, which is how the
// rehearsal tells a pre-migration database from an already-migrated one.
func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()

	rows, err := db.QueryContext(t.Context(),
		fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, table))
	if err != nil {
		t.Fatalf("inspecting %s: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("inspecting %s: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspecting %s: %v", table, err)
	}

	return false
}

// captureLegacy reads every pre-migration row, keyed the way the old schema keyed
// them.
func captureLegacy(t *testing.T, db *sql.DB) map[string]legacyCard {
	t.Helper()

	rows, err := db.QueryContext(t.Context(),
		`SELECT user_id, lang, lemma, stability, difficulty, state, reps, lapses, last_review, due
		 FROM study_progress`)
	if err != nil {
		t.Fatalf("reading the legacy deck: %v", err)
	}
	defer rows.Close()

	out := map[string]legacyCard{}
	for rows.Next() {
		var c legacyCard
		if err := rows.Scan(&c.user, &c.lang, &c.lemma, &c.stability, &c.difficulty,
			&c.state, &c.reps, &c.lapses, &c.lastReview, &c.due); err != nil {
			t.Fatalf("scanning a legacy row: %v", err)
		}
		out[c.user+"\n"+c.lang+"\n"+c.lemma] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the legacy deck: %v", err)
	}

	return out
}

// TestRehearseDeckMigrationAgainstARealDatabase is the check that matters before a
// migration ships: every card in a real deck must come out the far side with its
// scheduling untouched, in the deck it was always implicitly in.
//
// Losing here means a live learner signs in to a deck reset to zero, with months
// of FSRS state still in the file but unreachable.
func TestRehearseDeckMigrationAgainstARealDatabase(t *testing.T) {
	db, src := rehearsalDB(t)

	if !hasColumn(t, db, "study_progress", "user_id") {
		t.Skipf("%s has no study_progress table; nothing to rehearse", src)
	}

	if hasColumn(t, db, "study_progress", "deck") {
		t.Skipf("%s is already migrated; rehearse against a backup taken before the deploy", src)
	}

	before := captureLegacy(t, db)
	t.Logf("rehearsing against %d card(s) from %s", len(before), src)

	if len(before) == 0 {
		t.Skip("the nominated database holds no cards; rehearse against one with real data")
	}

	store, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}

	// Every captured row must be findable under its new key, byte for byte.
	var checked int
	for _, c := range before {
		got, ok, err := store.Get(t.Context(),
			mustParseUser(t, c.user), mustParseLang(t, c.lang), deckid.DerDieDas, c.lemma)
		if err != nil {
			t.Fatalf("card %q after migration: %v", c.lemma, err)
		}
		if !ok {
			t.Errorf("card %q vanished in the migration", c.lemma)
			continue
		}

		if got.Stability != c.stability || got.Difficulty != c.difficulty {
			t.Errorf("card %q: scalars changed, got stability=%v difficulty=%v want %v/%v",
				c.lemma, got.Stability, got.Difficulty, c.stability, c.difficulty)
		}
		if got.State.String() != c.state || got.Reps != c.reps || got.Lapses != c.lapses {
			t.Errorf("card %q: counters changed, got state=%s reps=%d lapses=%d want %s/%d/%d",
				c.lemma, got.State, got.Reps, got.Lapses, c.state, c.reps, c.lapses)
		}
		if got.Due.Format(timeLayoutForTest) != c.due {
			t.Errorf("card %q: due changed, got %s want %s",
				c.lemma, got.Due.Format(timeLayoutForTest), c.due)
		}

		checked++
	}

	t.Logf("verified %d card(s) survived with identical scheduling", checked)

	// Nothing may have been invented either: a migration that duplicated rows would
	// pass every check above.
	var after int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM study_progress`).Scan(&after); err != nil {
		t.Fatalf("counting after the migration: %v", err)
	}
	if after != len(before) {
		t.Errorf("deck holds %d cards after the migration, want %d", after, len(before))
	}

	// Open runs on every start, so re-running it must change nothing.
	for i := range 2 {
		if _, err := sqlitedb.Open(t.Context(), db); err != nil {
			t.Fatalf("re-Open #%d: %v", i+1, err)
		}
	}

	var reopened int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM study_progress`).Scan(&reopened); err != nil {
		t.Fatalf("counting after re-opening: %v", err)
	}
	if reopened != len(before) {
		t.Errorf("deck holds %d cards after repeated opens, want %d", reopened, len(before))
	}
}

// TestRollbackToTheOldBinaryIsSilentlyBroken pins down what happens if a deploy
// migrates the database and is then rolled back.
//
// deploy.sh restores the previous *binary* and never the database, so the old
// build meets a schema it does not know. The dangerous part is not that it breaks
// — it is *how* it breaks:
//
//   - the old build's CREATE TABLE IF NOT EXISTS is a no-op, so it starts cleanly;
//   - /healthz is wired to db.PingContext, which proves the connection is alive and
//     touches no table, so the probe returns 200;
//   - every actual study query fails, because the lemma column is gone.
//
// So deploy.sh's health check passes and reports a successful rollback while the
// app is completely unusable. This test asserts that shape deliberately, so the
// restore procedure in DEPLOYING.md has something holding it in place: if a future
// change makes the failure loud instead, this test fails and the procedure can be
// simplified.
func TestRollbackToTheOldBinaryIsSilentlyBroken(t *testing.T) {
	db, _ := rehearsalDB(t)

	if hasColumn(t, db, "study_progress", "deck") {
		t.Skip("the nominated database is already migrated; this test needs a pre-migration backup")
	}

	if _, err := sqlitedb.Open(t.Context(), db); err != nil {
		t.Fatalf("Open (which migrates): %v", err)
	}

	// What the old binary's health check does.
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping failed on a migrated database: %v", err)
	}

	// What the old binary's List does. If this ever stops failing, the premise of
	// the restore procedure has changed.
	_, err := db.QueryContext(t.Context(),
		`SELECT user_id, lang, lemma, stability, difficulty, state, reps, lapses, last_review, due
		 FROM study_progress WHERE user_id = ? AND lang = ?`, "local", "de")
	if err == nil {
		t.Fatal("the old binary's query still works against a migrated database -- " +
			"the rollback hazard documented in DEPLOYING.md no longer applies, update it")
	}

	t.Logf("confirmed: ping succeeds (so /healthz answers 200) while the deck query fails with %v", err)
}

// timeLayoutForTest mirrors the store's storage layout. It is written out rather
// than borrowed: the package's own constant is unexported, and a test asserting on
// stored text should state the format it expects rather than inherit whatever the
// code currently uses.
const timeLayoutForTest = "2006-01-02T15:04:05.999999999Z07:00"

func mustParseUser(t *testing.T, s string) userid.UserID {
	t.Helper()

	u, err := userid.Parse(s)
	if err != nil {
		t.Fatalf("stored user id %q does not parse: %v", s, err)
	}

	return u
}

func mustParseLang(t *testing.T, s string) langcode.LangCode {
	t.Helper()

	l, err := langcode.Parse(s)
	if err != nil {
		t.Fatalf("stored language %q does not parse: %v", s, err)
	}

	return l
}
