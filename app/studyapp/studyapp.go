// Package studyapp is the App layer: it turns HTTP requests into calls on the
// vocab and study Business domains and serves the browser client.
//
// This is the one layer allowed to know about both domains, and composing them
// is its whole job. studybus schedules opaque lemmas and never sees a noun;
// vocabbus knows nouns and never sees a schedule. Here the two meet: a batch
// request loads the deck from vocab, asks study which lemmas are due, and pairs
// the answers back together for the client. Nothing below this layer imports
// net/http, and nothing here reaches past the Business ports into a store.
package studyapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/errs"
)

// nounDeck is the deck every request handled here studies.
//
// The API does not name a deck yet: a client asks for a language and gets the
// German gender deck, because it is the only one that exists. The scheduler
// underneath is already deck-aware, so naming the deck here — once, in the layer
// that composes domains — is what keeps that true while the wire format stays
// exactly as it was. Adding a deck parameter later is a change to this file.
var nounDeck = deckid.DerDieDas

// Config wires an App together from its Business domains and its policy.
type Config struct {
	Vocab      *vocabbus.Business
	Study      *studybus.Business
	Curriculum *curriculumbus.Business
	BatchLimit int              // cards per preloaded batch; defaults to 20.
	Now        func() time.Time // clock seam; defaults to time.Now.
	Static     fs.FS            // embedded browser client; nil serves API only.

	// Log receives one line per request. Nil disables request logging, which is
	// what the tests want.
	Log *slog.Logger

	// Auth resolves a request to a learner. It is an interface declared here and
	// satisfied by the auth app, so this package never imports that one — the
	// layering forbids an App package importing another App package, and main
	// does the wiring instead.
	Auth Authenticator

	// Health is polled by GET /healthz to decide whether the app can actually do
	// its job, rather than merely being listened on. It is a func rather than a
	// method on a store because the store is reached only through a Business port
	// here, and liveness is not a Business concern; main owns the concrete store
	// and passes its check in. Nil means /healthz reports on process liveness
	// alone.
	Health func(context.Context) error
}

// App holds the wired dependencies and serves HTTP.
type App struct {
	vocab      *vocabbus.Business
	study      *studybus.Business
	curriculum *curriculumbus.Business
	batchLimit int
	now        func() time.Time
	static     fs.FS
	etags      map[string]string
	log        *slog.Logger
	health     func(context.Context) error
	auth       Authenticator
}

// Authenticator resolves an HTTP request to the learner making it. Returning
// false means "nobody is signed in", which every study route treats as 401.
type Authenticator interface {
	UserForRequest(r *http.Request) (userid.UserID, bool)
}

// SingleUser is an Authenticator that hands every request the built-in local
// learner. It is what the tests use, and what the -single-user development flag
// wires, so that running the deck without a mail server is still possible. It
// must never be wired on a public deployment: it makes every visitor the same
// person.
type SingleUser struct{}

// UserForRequest implements Authenticator.
func (SingleUser) UserForRequest(*http.Request) (userid.UserID, bool) {
	return userid.Local(), true
}

// New constructs an App, filling in defaults for the optional policy knobs.
func New(cfg Config) *App {
	limit := cfg.BatchLimit
	if limit <= 0 {
		limit = 20
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &App{
		vocab:      cfg.Vocab,
		study:      cfg.Study,
		curriculum: cfg.Curriculum,
		etags:      assetETags(cfg.Static),
		batchLimit: limit,
		now:        now,
		static:     cfg.Static,
		log:        cfg.Log,
		health:     cfg.Health,
		auth:       cfg.Auth,
	}
}

// Handler builds the HTTP routing for the app: the JSON API under /api and, when
// a static filesystem was provided, the browser client at the root.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/decks", a.handleDecks)
	mux.HandleFunc("GET /api/batch", a.handleBatch)
	mux.HandleFunc("POST /api/grade", a.handleGrade)
	mux.HandleFunc("GET /api/summary", a.handleSummary)
	mux.HandleFunc("GET /healthz", a.handleHealthz)

	if a.static != nil {
		// "GET /" is the least specific pattern, so the routes above still win.
		mux.Handle("GET /", a.staticHandler())
	}

	return mux
}

// staticHandler serves the embedded client.
//
// It exists to defuse one behaviour of http.FileServer: a request for
// "/index.html" is answered with a 301 to "./" to canonicalise the URL. On its own
// that is harmless, but behind a reverse proxy whose DirectoryIndex maps "/" onto
// index.html — which Apache does by default — the redirect points back at the
// request that produced it, and the home page becomes an infinite loop. The app
// was live and every other route worked; only "/" was unreachable.
//
// Mapping the path to "/" internally serves the same bytes with a 200 and leaves
// nothing for a proxy to disagree with.
// It also gives every asset a content ETag and asks browsers to revalidate.
//
// The client is three files that only work as a set: index.html declares the
// element ids app.js reaches for. Served from an embed.FS they carry no
// Last-Modified — the modtimes are zero — and http.FileServer adds no validator
// of its own, which leaves a browser free to apply heuristic freshness and hold
// any one of them for as long as it likes. A visitor who kept index.html across a
// release where app.js changed gets a client whose script asks for elements the
// page no longer has, and the app dies on the first null.
//
// "no-cache" is revalidate-before-use, not don't-store: with the ETag, an
// unchanged file costs a 304 and no body, and a changed one can never be paired
// with a stale sibling.
func (a *App) staticHandler() http.Handler {
	files := http.FileServer(http.FS(a.static))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tag, ok := a.etags[assetName(r.URL.Path)]; ok {
			// Set before delegating: http.ServeContent reads the ETag back off the
			// response header to answer If-None-Match, so this is what turns a
			// revalidation into a 304 rather than a re-send.
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
		}

		if r.URL.Path == "/index.html" {
			// Clone rather than mutate: the request is not ours to modify, and the
			// logging middleware still reports the path as it was asked for.
			clone := r.Clone(r.Context())
			clone.URL.Path = "/"
			r = clone
		}

		files.ServeHTTP(w, r)
	})
}

// assetName maps a request path to the name the asset has inside the filesystem.
// Both "/" and "/index.html" reach the same bytes, so both have to reach the same
// ETag — otherwise the home page would be the one file with no validator.
func assetName(path string) string {
	if path == "/" || path == "" {
		return "index.html"
	}

	return strings.TrimPrefix(path, "/")
}

// assetETags hashes every embedded asset once at construction, so serving one
// costs no hashing and two files can never disagree about which release they are
// from. A nil filesystem yields a nil map, and a nil map simply never matches.
//
// A read failure drops that file from the map rather than failing construction:
// the asset then serves without a validator, exactly as it did before, which is a
// weaker guarantee and not a broken app.
func assetETags(fsys fs.FS) map[string]string {
	if fsys == nil {
		return nil
	}

	out := map[string]string{}

	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil
		}

		// Half a SHA-256 is far more than enough to distinguish the handful of
		// files in one build, and it keeps the header short. Quoted because an
		// ETag without quotes is not a valid one.
		sum := sha256.Sum256(b)
		out[name] = `"` + hex.EncodeToString(sum[:8]) + `"`

		return nil
	})

	return out
}

// handleHealthz reports whether the app can serve, not merely whether it is
// listening: with a Health check wired it touches the store, so a database that
// has gone away fails the probe instead of returning 200 while every study
// request errors.
func (a *App) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if a.health != nil {
		if err := a.health(r.Context()); err != nil {
			// The body is deliberately vague; the detail goes to the log, not to
			// whoever is polling the endpoint.
			if a.log != nil {
				a.log.Error("health check failed", "err", err)
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)

			return
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// currentUser resolves the learner for a request, reporting false when nobody is
// signed in.
//
// A nil Auth means the app was constructed without an authenticator. That is
// treated as "no one is signed in" rather than as "everyone is the local user":
// forgetting to wire authentication should close the app, not open it.
func (a *App) currentUser(r *http.Request) (userid.UserID, bool) {
	if a.auth == nil {
		return userid.UserID{}, false
	}

	return a.auth.UserForRequest(r)
}

// requireUser resolves the learner or writes a 401 and reports false, so each
// handler's first two lines are the whole authorisation story.
func (a *App) requireUser(w http.ResponseWriter, r *http.Request) (userid.UserID, bool) {
	user, ok := a.currentUser(r)
	if !ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "sign in to study"})

		return userid.UserID{}, false
	}

	return user, true
}

// handleDecks answers with the whole shelf: every deck in the language's course,
// in order, each with the learner's standing in it and whether it is open yet.
//
// This is the one endpoint that composes all three domains — the catalog says
// what exists and what gates what, vocab sizes the deck whose items it owns, and
// study says how far the learner has got in each. It is also read-only: unlocking
// a deck is a fact derived here on every request, never a row someone writes, so
// there is no state to get out of step with the course.
func (a *App) handleDecks(w http.ResponseWriter, r *http.Request) {
	lang, err := toBusLang(r.URL.Query().Get("lang"))
	if err != nil {
		writeError(w, err)
		return
	}

	ctx := r.Context()
	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	// An App built without a curriculum has no shelf to show. Answering 500 rather
	// than dereferencing keeps a wiring mistake a legible error in the log instead
	// of a stack trace and a dropped connection. It sits after the authorisation
	// check so a signed-out visitor is still told they are signed out.
	if a.curriculum == nil {
		writeServerError(w, errors.New("studyapp: no curriculum wired"))
		return
	}

	decks, err := a.curriculum.Catalog(ctx, lang)
	if err != nil {
		writeServerError(w, err)
		return
	}

	now := a.now()

	// Two passes. The first collects each deck's own numbers; the second turns
	// them into gates, which it can only do once every deck's coverage is known —
	// a deck's gate is a statement about a *different* deck's progress. The
	// catalog guarantees a prerequisite appears earlier, so one pass would in fact
	// suffice today, but relying on that would make the ordering rule load-bearing
	// in two places instead of one.
	type standing struct {
		size     int
		learned  int
		dueNow   int
		coverage curriculumbus.Coverage
	}

	standings := make(map[deckid.DeckID]standing, len(decks))
	titles := make(map[deckid.DeckID]string, len(decks))

	for _, d := range decks {
		size, err := a.deckSize(ctx, d)
		if err != nil {
			writeServerError(w, err)
			return
		}

		progress, err := a.study.Progress(ctx, user, lang, d.ID)
		if err != nil {
			writeServerError(w, err)
			return
		}

		dueNow := 0
		for _, p := range progress {
			if !p.IsNew() && !p.Due.After(now) {
				dueNow++
			}
		}

		standings[d.ID] = standing{
			size:     size,
			learned:  len(progress),
			dueNow:   dueNow,
			coverage: curriculumbus.Coverage{Size: size, Seen: len(progress)},
		}
		titles[d.ID] = d.Title
	}

	out := make([]deckResponse, 0, len(decks))
	for _, d := range decks {
		s := standings[d.ID]
		gate := a.curriculum.Gate(d, standings[d.Prerequisite].coverage)
		out = append(out, fromBusDeckResponse(d, gate, titles[d.Prerequisite], s.size, s.learned, s.dueNow))
	}

	writeJSON(w, http.StatusOK, catalogResponse{Lang: lang.String(), Decks: out})
}

// deckSize reports how many items a deck holds.
//
// The catalog carries the items of the decks it was written alongside and counts
// them itself; the noun deck's items predate the catalog and still live in the
// vocab domain, so its entry reports 0 and the count comes from there. This is
// the one place that knows which source answers for which deck, which is exactly
// the composing layer's job — no Business package has to learn about the other.
func (a *App) deckSize(ctx context.Context, d curriculumbus.Deck) (int, error) {
	if d.Size > 0 {
		return d.Size, nil
	}

	nouns, err := a.vocab.Deck(ctx, d.Lang)
	if err != nil {
		return 0, err
	}

	return len(nouns), nil
}

// handleBatch preloads a study session: the ordered, ready-to-swipe cards for a
// language, each carrying its correct article so the client can grade locally.
func (a *App) handleBatch(w http.ResponseWriter, r *http.Request) {
	lang, err := toBusLang(r.URL.Query().Get("lang"))
	if err != nil {
		writeError(w, err)
		return
	}

	ctx := r.Context()
	deck, err := a.vocab.Deck(ctx, lang)
	if err != nil {
		writeServerError(w, err)
		return
	}

	byLemma := make(map[string]vocabbus.Noun, len(deck))
	lemmas := make([]string, len(deck))
	for i, n := range deck {
		byLemma[n.Lemma] = n
		lemmas[i] = n.Lemma
	}

	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	chosen, err := a.study.Batch(ctx, user, lang, nounDeck, lemmas, a.now(), a.batchLimit)
	if err != nil {
		writeServerError(w, err)
		return
	}

	cards := make([]batchCardResponse, 0, len(chosen))
	for _, lemma := range chosen {
		if n, ok := byLemma[lemma]; ok {
			cards = append(cards, fromBusNounResponse(n))
		}
	}

	writeJSON(w, http.StatusOK, batchResponse{Lang: lang.String(), Cards: cards})
}

// handleGrade applies a batched flush of graded cards and returns the deck's
// state afterward. Grading is applied card by card; the first storage failure
// stops the flush and reports how many were applied before it.
func (a *App) handleGrade(w http.ResponseWriter, r *http.Request) {
	var req gradeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	lang, results, err := toBusGradeRequest(req)
	if err != nil {
		writeError(w, err)
		return
	}

	ctx := r.Context()
	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	now := a.now()

	applied := 0
	for _, res := range results {
		// roleanswer.None: this deck asks for an article and nothing else. The
		// two-part cards that have a role half live in a deck that does not exist yet.
		if _, err := a.study.Grade(ctx, user, lang, nounDeck, res.lemma, res.rating, roleanswer.None, now); err != nil {
			writeServerError(w, err)
			return
		}
		applied++
	}

	sum, err := a.computeSummary(ctx, user, lang)
	if err != nil {
		writeServerError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, gradeResponse{
		Applied:  applied,
		DueNow:   sum.DueNow,
		Learned:  sum.Learned,
		DeckSize: sum.DeckSize,
	})
}

// handleSummary reports deck size and the learner's progress for a language.
func (a *App) handleSummary(w http.ResponseWriter, r *http.Request) {
	lang, err := toBusLang(r.URL.Query().Get("lang"))
	if err != nil {
		writeError(w, err)
		return
	}

	user, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	sum, err := a.computeSummary(r.Context(), user, lang)
	if err != nil {
		writeServerError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, sum)
}

// computeSummary tallies deck size, cards ever learned, and cards due right now.
// It is shared by the grade flush and the summary endpoint so both report the
// same numbers.
func (a *App) computeSummary(ctx context.Context, user userid.UserID, lang langcode.LangCode) (summaryResponse, error) {
	deck, err := a.vocab.Deck(ctx, lang)
	if err != nil {
		return summaryResponse{}, err
	}

	progress, err := a.study.Progress(ctx, user, lang, nounDeck)
	if err != nil {
		return summaryResponse{}, err
	}

	now := a.now()
	dueNow := 0
	for _, p := range progress {
		if !p.IsNew() && !p.Due.After(now) {
			dueNow++
		}
	}

	return summaryResponse{
		Lang:     lang.String(),
		DeckSize: len(deck),
		Learned:  len(progress),
		DueNow:   dueNow,
	}, nil
}

// --- HTTP plumbing ---------------------------------------------------------

// decodeJSON reads a JSON request body into v, rejecting unknown fields so a
// typo'd client key is an error rather than a silently ignored no-op.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	return dec.Decode(v)
}

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError maps a caller-input failure to 400. A FieldErrors becomes a
// per-field map so the client can point at what it sent wrong; any other error
// here is treated as a bad request with its message.
func writeError(w http.ResponseWriter, err error) {
	if fes, ok := errs.IsFieldErrors(err); ok {
		fields := make(map[string]string, len(fes))
		for _, fe := range fes {
			fields[fe.Field] = fe.Err.Error()
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request", "fields": fields})
		return
	}

	writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

// writeServerError maps an internal failure to 500 without leaking its detail
// into the response body beyond a generic message; the detail is the operator's
// to read from logs, not the client's.
func writeServerError(w http.ResponseWriter, _ error) {
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
}
