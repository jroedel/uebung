// Command uebung serves the der/die/das practice app: an HTTP server that hands
// a browser preloaded batches of German nouns and schedules them with FSRS.
//
// It wires the layers together and nothing more. The deck comes from the
// embedded seed store; a learner's progress is persisted to SQLite via sqlitedb.
// That store is reached only through studybus.Storer, so replacing it is a
// change to the constructor below and nothing else in this file, or in the
// business or app layers, moves.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/jroedel/uebung/app/studyapp"
	"github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/vocab/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/foundation/fsrs"
)

func main() {
	if err := run(); err != nil {
		slog.Error("uebung exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", ":8080", "address to listen on")
	dataPath := flag.String("data", "uebung.db", "path to the progress database")
	batchLimit := flag.Int("batch", 20, "cards per preloaded batch")
	retention := flag.Float64("retention", 0.9, "FSRS desired retention, in (0,1)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Cancelled on interrupt. Established before the store so opening the database
	// is itself interruptible, and reused for the graceful shutdown below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Storage: SQLite. The studybus.Storer contract is all the rest of the program
	// knows, so this constructor is the only line another backend would replace.
	store, err := sqlitedb.Open(ctx, *dataPath)
	if err != nil {
		return fmt.Errorf("opening progress store: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Error("closing progress store", "err", err)
		}
	}()

	// Business.
	vocab := vocabbus.NewBusiness(seeddb.New())

	fsrsParams := fsrs.Default()
	fsrsParams.DesiredRetention = *retention
	study := studybus.NewBusiness(store, studybus.Config{FSRS: fsrsParams})

	// App.
	app := studyapp.New(studyapp.Config{
		Vocab:      vocab,
		Study:      study,
		BatchLimit: *batchLimit,
		Now:        time.Now,
		Static:     studyapp.Assets(),
		Log:        log,
		Health:     store.Ping,
	})

	// Timeouts are set for an internet-facing deployment behind a reverse proxy.
	// ReadHeaderTimeout alone left a connection able to dawdle indefinitely once
	// the headers were in.
	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Serve until the interrupt context is cancelled, then shut down gracefully.
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", *addr, "data", *dataPath)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
