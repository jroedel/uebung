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
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/errs"
)

// Config wires an App together from its two Business domains and its policy.
type Config struct {
	Vocab      *vocabbus.Business
	Study      *studybus.Business
	BatchLimit int              // cards per preloaded batch; defaults to 20.
	Now        func() time.Time // clock seam; defaults to time.Now.
	Static     fs.FS            // embedded browser client; nil serves API only.
}

// App holds the wired dependencies and serves HTTP.
type App struct {
	vocab      *vocabbus.Business
	study      *studybus.Business
	batchLimit int
	now        func() time.Time
	static     fs.FS
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
		batchLimit: limit,
		now:        now,
		static:     cfg.Static,
	}
}

// Handler builds the HTTP routing for the app: the JSON API under /api and, when
// a static filesystem was provided, the browser client at the root.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/batch", a.handleBatch)
	mux.HandleFunc("POST /api/grade", a.handleGrade)
	mux.HandleFunc("GET /api/summary", a.handleSummary)

	if a.static != nil {
		mux.Handle("GET /", http.FileServer(http.FS(a.static)))
	}

	return mux
}

// currentUser resolves the learner for a request. Today it is always the built-in
// single user; this is the single seam where session- or token-derived identity
// will attach when accounts arrive, and every downstream call already takes the
// UserID it returns.
func (a *App) currentUser(_ *http.Request) userid.UserID {
	return userid.Local()
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

	user := a.currentUser(r)
	chosen, err := a.study.Batch(ctx, user, lang, lemmas, a.now(), a.batchLimit)
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
	user := a.currentUser(r)
	now := a.now()

	applied := 0
	for _, res := range results {
		if _, err := a.study.Grade(ctx, user, lang, res.lemma, res.rating, now); err != nil {
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

	sum, err := a.computeSummary(r.Context(), a.currentUser(r), lang)
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

	progress, err := a.study.Progress(ctx, user, lang)
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
