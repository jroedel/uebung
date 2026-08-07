// Command uebung-claim hands the pre-accounts deck to a real account.
//
// Before sign-in existed, every study record was keyed to the built-in "local"
// learner. Once accounts are required those rows are still in the database but
// unreachable: you sign in and find a fresh deck, with months of FSRS scheduling
// stranded behind a user id nobody can log in as. This moves them.
//
// It is a separate command rather than a flag on the server on purpose. Re-keying
// every row of somebody's progress is not something that should be one typo away
// from happening on a routine restart, and running it needs the server stopped
// anyway — SQLite takes one writer.
//
// Usage:
//
//	uebung-claim -data uebung.db -email you@example.com
//
// The account must already exist, which means signing in once first. That is the
// safety check: it proves the address is yours before the deck is handed over.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/sqldb"

	identitydb "github.com/jroedel/uebung/business/domain/identity/stores/sqlitedb"
	studydb "github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
)

func main() {
	if err := run(); err != nil {
		slog.Error("uebung-claim failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	dataPath := flag.String("data", "uebung.db", "path to the SQLite database")
	addr := flag.String("email", "", "address of the account that should own the existing deck")
	from := flag.String("from", "local", "learner id whose rows are being claimed")
	dryRun := flag.Bool("dry-run", false, "report what would move without moving it")
	flag.Parse()

	if *addr == "" {
		return fmt.Errorf("-email is required")
	}

	target, err := email.Parse(*addr)
	if err != nil {
		return fmt.Errorf("parsing -email: %w", err)
	}

	source, err := userid.Parse(*from)
	if err != nil {
		return fmt.Errorf("parsing -from: %w", err)
	}

	ctx := context.Background()

	db, err := sqldb.Open(ctx, *dataPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	identityStore, err := identitydb.Open(ctx, db)
	if err != nil {
		return fmt.Errorf("preparing identity store: %w", err)
	}

	studyStore, err := studydb.Open(ctx, db)
	if err != nil {
		return fmt.Errorf("preparing study store: %w", err)
	}

	user, found, err := identityStore.UserByEmail(ctx, target)
	if err != nil {
		return fmt.Errorf("looking up the account: %w", err)
	}
	if !found {
		return fmt.Errorf("no account for %s yet — sign in once first, then re-run this", target)
	}
	if !user.Verified {
		return fmt.Errorf("the account for %s has never followed a sign-in link, so it is not proven to be yours", target)
	}

	pending, err := studyStore.CountFor(ctx, source)
	if err != nil {
		return fmt.Errorf("counting the existing deck: %w", err)
	}

	existing, err := studyStore.CountFor(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("counting the account's deck: %w", err)
	}

	fmt.Printf("%d card(s) held by %q\n%d card(s) already owned by %s\n", pending, source, existing, target)

	if pending == 0 {
		fmt.Println("nothing to move")

		return nil
	}

	if *dryRun {
		fmt.Println("dry run: nothing was changed")

		return nil
	}

	moved, err := studyStore.Reassign(ctx, source, user.ID)
	if err != nil {
		return fmt.Errorf("reassigning the deck: %w", err)
	}

	// A shortfall is not an error: a card the account had already studied keeps
	// the account's own, newer scheduling rather than being overwritten by the
	// anonymous one. Reporting it makes that visible instead of surprising.
	fmt.Printf("moved %d card(s) to %s\n", moved, target)
	if skipped := pending - moved; skipped > 0 {
		fmt.Printf("%d card(s) were left behind because %s already had progress on them\n", skipped, target)
	}

	return nil
}
