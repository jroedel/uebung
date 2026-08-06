// Command uebung serves the der/die/das practice app: an HTTP server that hands
// a browser preloaded batches of German nouns and schedules them with FSRS.
//
// It wires the layers together and nothing more. The deck comes from the
// embedded seed store; a learner's progress is persisted to a JSON file via
// filedb — the stdlib-only default. Swapping in a SQLite-backed store later is a
// change to exactly the store constructor below: it implements the same
// studybus.Storer, so nothing else in this file, or in the business or app
// layers, moves.
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
	"github.com/jroedel/uebung/business/domain/study/stores/filedb"
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
	dataPath := flag.String("data", "uebung-data.json", "path to the progress store file")
	batchLimit := flag.Int("batch", 20, "cards per preloaded batch")
	retention := flag.Float64("retention", 0.9, "FSRS desired retention, in (0,1)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Storage: the stdlib file store. This is the one line a SQLite store would
	// replace; the studybus.Storer contract is all the rest of the program knows.
	store, err := filedb.Open(*dataPath)
	if err != nil {
		return fmt.Errorf("opening progress store: %w", err)
	}

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
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Serve until an interrupt, then shut down gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

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
