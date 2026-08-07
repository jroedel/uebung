// Command uebung serves Übung Club: an HTTP server that hands a browser
// preloaded batches of German nouns and schedules them with FSRS.
//
// It wires the layers together and nothing more. The deck comes from the embedded
// seed store; accounts and progress are persisted to one SQLite file, opened once
// here and shared by both domain stores so a single writer stays a single writer.
//
// Identity is passwordless: the auth app emails a one-time link and exchanges it
// for a session cookie, and the study app is handed that app as its Authenticator.
// The two App packages never import each other — this file is the seam.
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
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jroedel/uebung/app/authapp"
	"github.com/jroedel/uebung/app/studyapp"
	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/mailers/loginmail"
	"github.com/jroedel/uebung/business/domain/identity/nicknamer"
	identitydb "github.com/jroedel/uebung/business/domain/identity/stores/sqlitedb"
	studydb "github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/vocab/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
	"github.com/jroedel/uebung/foundation/mailer"
	"github.com/jroedel/uebung/foundation/sqldb"
	"github.com/jroedel/uebung/foundation/web"
)

const appName = "Übung Club"

func main() {
	if err := run(); err != nil {
		slog.Error("uebung exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on; keep it on loopback behind a TLS proxy")
	dataPath := flag.String("data", "uebung.db", "path to the SQLite database")
	batchLimit := flag.Int("batch", 20, "cards per preloaded batch")
	retention := flag.Float64("retention", 0.9, "FSRS desired retention, in (0,1)")

	linkBase := flag.String("link-base", "", "absolute URL of the sign-in callback, e.g. https://uebung.club/auth/callback")
	trustProxy := flag.Bool("trust-proxy", false, "believe X-Forwarded-For for rate limiting; only with a trusted proxy in front")
	insecureCookies := flag.Bool("insecure-cookies", false, "DEVELOPMENT ONLY: issue session cookies without Secure, for plain-HTTP localhost")
	singleUser := flag.Bool("single-user", false, "DEVELOPMENT ONLY: skip sign-in and make every visitor the same learner")

	smtpHost := flag.String("smtp-host", "", "SMTP host; empty logs the sign-in link instead of sending it")
	smtpPort := flag.Int("smtp-port", 587, "SMTP port")
	smtpUser := flag.String("smtp-user", "", "SMTP username")
	mailFrom := flag.String("mail-from", "", "From: address for sign-in mail")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Cancelled on interrupt. Established before the store so opening the database
	// is itself interruptible, and reused for the graceful shutdown below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Storage: one handle, two domains.
	db, err := sqldb.Open(ctx, *dataPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database", "err", err)
		}
	}()

	studyStore, err := studydb.Open(ctx, db)
	if err != nil {
		return fmt.Errorf("preparing study store: %w", err)
	}

	identityStore, err := identitydb.Open(ctx, db)
	if err != nil {
		return fmt.Errorf("preparing identity store: %w", err)
	}

	// Business.
	vocab := vocabbus.NewBusiness(seeddb.New())

	fsrsParams := fsrs.Default()
	fsrsParams.DesiredRetention = *retention
	study := studybus.NewBusiness(studyStore, studybus.Config{FSRS: fsrsParams})

	// The password is read from the environment rather than a flag: flags are
	// visible in ps output to every user on a shared machine.
	sender := buildSender(log, *smtpHost, *smtpPort, *smtpUser, os.Getenv("UEBUNG_SMTP_PASSWORD"), *mailFrom)

	// The word lists are validated as they load, so a badly edited entry stops
	// the server here with the offending word named rather than surfacing later
	// as a name some fraction of learners cannot be given.
	namer, err := nicknamer.New(nicknamer.Config{})
	if err != nil {
		return fmt.Errorf("preparing nicknames: %w", err)
	}

	identity, err := identitybus.NewBusiness(identitybus.Config{
		Storer:   identityStore,
		Mailer:   loginmail.New(sender, appName, 15*time.Minute),
		Namer:    namer,
		LinkBase: *linkBase,
		NewID:    newUserID,
		Now:      time.Now,
	})
	if err != nil {
		return fmt.Errorf("preparing identity: %w", err)
	}

	// Cookie security is decided once, here, from the scheme of the link we put in
	// emails — not per request from X-Forwarded-Proto. Inferring it per request
	// meant a deployment behind Apache (r.TLS nil, and mod_proxy_http does not
	// send X-Forwarded-Proto) silently issued 90-day session cookies with neither
	// Secure nor the __Host- prefix, with no visible symptom.
	secureCookies := strings.HasPrefix(*linkBase, "https://") && !*insecureCookies
	if !secureCookies {
		log.Warn("session cookies will be issued WITHOUT Secure: only acceptable on plain-HTTP localhost",
			"link_base", *linkBase, "insecure_cookies", *insecureCookies)
	}

	// App.
	auth := authapp.New(authapp.Config{
		Identity:      identity,
		Log:           log,
		Now:           time.Now,
		TrustProxy:    *trustProxy,
		SecureCookies: secureCookies,
	})

	// The study app takes an Authenticator interface; the auth app satisfies it.
	// -single-user swaps in the stub that makes everyone the built-in local
	// learner, which is how the deck stays runnable without a mail server.
	var authenticator studyapp.Authenticator = auth
	if *singleUser {
		log.Warn("running in single-user mode: every visitor is the same learner, do not expose this")
		authenticator = studyapp.SingleUser{}
	}

	app := studyapp.New(studyapp.Config{
		Vocab:      vocab,
		Study:      study,
		BatchLimit: *batchLimit,
		Now:        time.Now,
		Static:     studyapp.Assets(),
		Log:        log,
		Auth:       authenticator,
		Health:     db.PingContext,
	})

	// Routing: the auth app owns /auth/, the study app owns everything else. Go's
	// mux prefers the more specific pattern, so the study app's "GET /" catch-all
	// for the client does not swallow the sign-in routes.
	root := http.NewServeMux()
	root.Handle("/auth/", auth.Handler())
	root.Handle("/", app.Handler())

	// Expire spent tokens and sessions in the background. Housekeeping only:
	// every check treats expiry explicitly, so a missed sweep is untidy, not
	// unsafe.
	go purgeLoop(ctx, log, identity)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           web.Logging(log, time.Now, root),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", *addr, "data", *dataPath, "trust_proxy", *trustProxy)
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

// buildSender picks the mail transport. With no SMTP host configured it returns
// the logging sender, so a developer sees the magic link in the server output and
// can sign in with no mail server at all.
func buildSender(log *slog.Logger, host string, port int, user, password, from string) mailer.Sender {
	if host == "" {
		log.Warn("no -smtp-host configured: sign-in links will be written to the log, not emailed")

		return mailer.Log{Logger: log}
	}

	return mailer.SMTP{
		Host:     host,
		Port:     port,
		Username: user,
		Password: password,
		From:     from,
		FromName: appName,
	}
}

// newUserID mints an account identifier. A UUID rather than the email address:
// every study row is keyed by this, and putting an address in that key would
// spread personal data across the whole database and make changing it a
// migration.
func newUserID() (userid.UserID, error) {
	return userid.Parse(uuid.NewString())
}

// purgeLoop sweeps expired tokens and sessions once an hour.
func purgeLoop(ctx context.Context, log *slog.Logger, identity *identitybus.Business) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		if err := identity.Purge(ctx); err != nil && ctx.Err() == nil {
			log.Error("purging expired credentials", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
