package studyapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jroedel/uebung/app/studyapp"
	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	curriculumseed "github.com/jroedel/uebung/business/domain/curriculum/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/study/stores/memdb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	triggerseed "github.com/jroedel/uebung/business/domain/trigger/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/trigger/triggerbus"
	"github.com/jroedel/uebung/business/domain/vocab/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
)

// fixedNow pins the app's clock so scheduling is deterministic in tests.
func fixedNow() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) }

// newCurriculum builds the real catalog over the embedded files. Tests use the
// authored course rather than a stub on purpose: the shelf a learner sees is
// authored data, and a stub would pass while the files that ship were broken.
func newCurriculum(t *testing.T) *curriculumbus.Business {
	t.Helper()

	c, err := curriculumbus.NewBusiness(curriculumseed.New(), curriculumbus.Config{})
	if err != nil {
		t.Fatalf("curriculum: %v", err)
	}

	return c
}

func newServer(t *testing.T) http.Handler {
	t.Helper()
	vocab := vocabbus.NewBusiness(seeddb.New())
	study := studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()})
	app := studyapp.New(studyapp.Config{
		Vocab:      vocab,
		Trigger:    triggerbus.NewBusiness(triggerseed.New()),
		Study:      study,
		Curriculum: newCurriculum(t),
		BatchLimit: 5,
		Now:        fixedNow,
		Auth:       studyapp.SingleUser{},
	})
	return app.Handler()
}

// newServerWithStatic is newServer plus the embedded client, for the routes that
// only exist when assets are served.
func newServerWithStatic(t *testing.T) http.Handler {
	t.Helper()

	app := studyapp.New(studyapp.Config{
		Vocab:      vocabbus.NewBusiness(seeddb.New()),
		Trigger:    triggerbus.NewBusiness(triggerseed.New()),
		Study:      studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()}),
		Curriculum: newCurriculum(t),
		Now:        fixedNow,
		Auth:       studyapp.SingleUser{},
		Static:     studyapp.Assets(),
	})

	return app.Handler()
}

// healthServer builds an app whose health check returns whatever err is, so both
// the healthy and the degraded path can be exercised.
func healthServer(t *testing.T, err error) http.Handler {
	t.Helper()

	app := studyapp.New(studyapp.Config{
		Vocab:  vocabbus.NewBusiness(seeddb.New()),
		Study:  studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()}),
		Now:    fixedNow,
		Auth:   studyapp.SingleUser{},
		Health: func(context.Context) error { return err },
	})

	return app.Handler()
}

// A reverse proxy needs /healthz to mean "can serve", not "is listening". With no
// check wired it reports process liveness; with one, a failing store must fail
// the probe rather than returning 200 while every study request errors.
func TestHealthz(t *testing.T) {
	tests := map[string]struct {
		h    http.Handler
		want int
	}{
		"no check wired": {newServer(t), http.StatusOK},
		"store healthy":  {healthServer(t, nil), http.StatusOK},
		"store down":     {healthServer(t, errors.New("database is gone")), http.StatusServiceUnavailable},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body)
			}
			// The failure detail belongs in the log, not in the response body.
			if tc.want != http.StatusOK && strings.Contains(rec.Body.String(), "database is gone") {
				t.Errorf("health failure leaked the underlying error: %s", rec.Body)
			}
		})
	}
}

// The manifest has to arrive as application/manifest+json or the browser ignores
// it and the install prompt never appears — a failure with no visible symptom in
// the app itself, which is why it is pinned here.
func TestManifestAndIconsAreServed(t *testing.T) {
	h := newServerWithStatic(t)

	t.Run("manifest", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
			t.Fatalf("Content-Type = %q, want application/manifest+json", ct)
		}

		var m struct {
			Name     string `json:"name"`
			StartURL string `json:"start_url"`
			Display  string `json:"display"`
			Icons    []struct {
				Src     string `json:"src"`
				Sizes   string `json:"sizes"`
				Purpose string `json:"purpose"`
			} `json:"icons"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("manifest is not valid JSON: %v", err)
		}
		if m.Name != "Übung Club" || m.StartURL != "/" || m.Display != "standalone" {
			t.Errorf("manifest = %+v, want the installable Übung Club shape", m)
		}
		// Android crops icons; without a maskable one it crops the artwork.
		var maskable bool
		for _, i := range m.Icons {
			if i.Purpose == "maskable" {
				maskable = true
			}
		}
		if !maskable {
			t.Error("manifest declares no maskable icon")
		}
	})

	// Every icon the manifest names must actually exist, or the install silently
	// falls back to a screenshot of the page.
	for _, name := range []string{"icon-192.png", "icon-512.png", "icon-maskable-512.png"} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
				t.Fatalf("Content-Type = %q, want image/png", ct)
			}
		})
	}
}

// http.FileServer answers "/index.html" with a 301 to "./" to canonicalise the
// URL. Behind a proxy whose DirectoryIndex maps "/" onto index.html — Apache's
// default — that redirect points back at the request that caused it, and the home
// page becomes an infinite loop. It happened in production: every other route
// worked and only "/" was unreachable, 50 redirects deep.
// The client is three files that only work as a set: index.html declares the ids
// app.js reaches for. Embedded files carry no Last-Modified, so without a
// validator a browser is free to keep one of them across a release and pair it
// with a fresh sibling — which is a null dereference on the first line that looks
// for an element the cached page does not have.
func TestStaticAssetsCarryAValidatorAndRevalidate(t *testing.T) {
	h := newServerWithStatic(t)

	// "/" and "/index.html" reach the same bytes and must agree, or the home page
	// is the one file with no validator.
	for _, path := range []string{"/", "/index.html", "/app.js", "/styles.css", "/manifest.webmanifest"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}

			tag := rec.Header().Get("ETag")
			if tag == "" {
				t.Fatal("no ETag, so a browser may hold this file for as long as it likes")
			}
			if !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) {
				t.Errorf("ETag %s is not quoted, which makes it invalid", tag)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", got)
			}

			// The revalidation has to be cheap, or no-cache would mean re-sending
			// every asset on every page load.
			again := httptest.NewRequest(http.MethodGet, path, nil)
			again.Header.Set("If-None-Match", tag)
			rec2 := httptest.NewRecorder()
			h.ServeHTTP(rec2, again)

			if rec2.Code != http.StatusNotModified {
				t.Errorf("revalidation status = %d, want 304", rec2.Code)
			}
			if rec2.Body.Len() != 0 {
				t.Errorf("304 carried %d bytes of body", rec2.Body.Len())
			}
		})
	}
}

// Two different files must not share a validator, and the same bytes must always
// produce the same one — otherwise a 304 could hand back the wrong file, which is
// worse than no caching at all.
func TestAssetETagsFollowContent(t *testing.T) {
	h := newServerWithStatic(t)

	tagFor := func(path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		return rec.Header().Get("ETag")
	}

	index, app, css := tagFor("/"), tagFor("/app.js"), tagFor("/styles.css")

	if index == app || app == css || index == css {
		t.Errorf("different assets share an ETag: index=%s app=%s css=%s", index, app, css)
	}
	if root := tagFor("/index.html"); root != index {
		t.Errorf(`"/" and "/index.html" disagree: %s vs %s`, index, root)
	}
	if again := tagFor("/app.js"); again != app {
		t.Errorf("the same file produced two ETags: %s then %s", app, again)
	}
}

func TestIndexHTMLIsServedNotRedirected(t *testing.T) {
	h := newServerWithStatic(t)

	for _, path := range []string{"/", "/index.html"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; Location=%q", rec.Code, rec.Header().Get("Location"))
			}
			if !strings.Contains(rec.Body.String(), "Übung Club") {
				t.Errorf("%s did not serve the client HTML", path)
			}
		})
	}
}

// deckEntry mirrors one entry of the catalog response. It is spelled out here
// rather than shared with the app package on purpose: this is the wire contract a
// browser depends on, and a test that reused the server's own struct would keep
// passing through a rename that broke every client.
type deckEntry struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Drill    string   `json:"drill"`
	Answers  []string `json:"answers"`

	DeckSize int `json:"deck_size"`
	Learned  int `json:"learned"`
	DueNow   int `json:"due_now"`

	Unlocked      bool   `json:"unlocked"`
	Requires      string `json:"requires"`
	RequiresTitle string `json:"requires_title"`
	RequiresSeen  int    `json:"requires_seen"`
	RequiresNeed  int    `json:"requires_need"`

	IntroSeen bool `json:"intro_seen"`

	Intro struct {
		Heading string   `json:"heading"`
		Body    []string `json:"body"`
		Groups  []struct {
			Answer  string `json:"answer"`
			Label   string `json:"label"`
			Members string `json:"members"`
			Hook    string `json:"hook"`
		} `json:"groups"`
		Closing string `json:"closing"`
	} `json:"intro"`
}

func getDecks(t *testing.T, h http.Handler) []deckEntry {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/decks?lang=de", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var body struct {
		Lang  string      `json:"lang"`
		Decks []deckEntry `json:"decks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding catalog: %v", err)
	}

	if body.Lang != "de" {
		t.Fatalf("lang = %q, want de", body.Lang)
	}

	return body.Decks
}

// The catalog is what the picker is built from, so everything a deck card needs
// has to arrive in one response — including the introduction, which is wanted
// before the first session and again whenever it is reread.
func TestDecksListsTheCourse(t *testing.T) {
	decks := getDecks(t, newServer(t))

	if len(decks) < 2 {
		t.Fatalf("catalog holds %d deck(s), want the whole course", len(decks))
	}

	if decks[0].ID != "der-die-das" {
		t.Fatalf("first deck is %q, want der-die-das", decks[0].ID)
	}

	for _, d := range decks {
		if d.Title == "" || d.Subtitle == "" || d.Drill == "" {
			t.Errorf("deck %q is missing shelf text: %+v", d.ID, d)
		}
		if len(d.Answers) < 2 {
			t.Errorf("deck %q offers %d answer(s)", d.ID, len(d.Answers))
		}
		// A deck of size 0 renders as a progress bar over nothing and would make
		// the gate behind it open at once.
		if d.DeckSize == 0 {
			t.Errorf("deck %q reports no items", d.ID)
		}
		if d.Intro.Heading == "" || len(d.Intro.Body) == 0 || d.Intro.Closing == "" {
			t.Errorf("deck %q arrived without its introduction", d.ID)
		}
	}
}

// The noun deck's items live in the vocab domain, not in the catalog, so its size
// is the one the App has to go and fetch. Getting this wrong is invisible in the
// catalog itself and shows up only here.
func TestDecksSizesTheNounDeckFromVocab(t *testing.T) {
	decks := getDecks(t, newServer(t))

	nouns, err := vocabbus.NewBusiness(seeddb.New()).Deck(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("loading the noun deck: %v", err)
	}

	if decks[0].DeckSize != len(nouns) {
		t.Errorf("der-die-das reports %d items, want %d from the vocab deck", decks[0].DeckSize, len(nouns))
	}
}

// A learner who has done nothing sees exactly one open deck, and the ones behind
// it say what they are waiting for rather than simply being shut.
func TestDecksLockWhatComesNext(t *testing.T) {
	decks := getDecks(t, newServer(t))

	if !decks[0].Unlocked {
		t.Error("the first deck is locked; nothing gates it")
	}
	if decks[0].Requires != "" {
		t.Errorf("the first deck requires %q; nothing may gate it", decks[0].Requires)
	}
	if decks[0].IntroSeen {
		t.Error("a learner with no progress is treated as having seen the intro")
	}

	for _, d := range decks[1:] {
		if d.Unlocked {
			t.Errorf("deck %q is open to a learner who has studied nothing", d.ID)
		}
		if d.Requires == "" {
			t.Errorf("deck %q is locked but names no prerequisite", d.ID)
		}
		// Without the title the client can only say "locked", which tells a learner
		// nothing about what to go and do.
		if d.RequiresTitle == "" {
			t.Errorf("deck %q names prerequisite %q with no title", d.ID, d.Requires)
		}
		if d.RequiresNeed <= 0 {
			t.Errorf("deck %q needs %d items of %q, which cannot be right",
				d.ID, d.RequiresNeed, d.Requires)
		}
		if d.RequiresSeen != 0 {
			t.Errorf("deck %q reports %d items seen in %q for a learner with no progress",
				d.ID, d.RequiresSeen, d.Requires)
		}
	}
}

// Studying moves the numbers the picker draws its bars from, and it is what turns
// the introduction from something shown unprompted into something offered.
func TestDecksTrackProgressInTheDeckStudied(t *testing.T) {
	h := newServer(t)

	before := getDecks(t, h)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/grade",
		bytes.NewReader([]byte(`{"lang":"de","results":[{"lemma":"Mann","rating":"good"},{"lemma":"Frau","rating":"again"}]}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	after := getDecks(t, h)

	if after[0].Learned != 2 {
		t.Errorf("der-die-das reports %d learned after two graded cards, want 2", after[0].Learned)
	}
	if !after[0].IntroSeen {
		t.Error("the intro is still unseen after studying the deck")
	}
	if before[0].IntroSeen {
		t.Error("the intro was already seen before anything was studied")
	}

	// The other decks must not have moved: grading in one deck is not progress in
	// another, which is the whole reason study records grew a deck column.
	for _, d := range after[1:] {
		if d.Learned != 0 {
			t.Errorf("deck %q reports %d learned after studying der-die-das", d.ID, d.Learned)
		}

		// Each deck watches its own prerequisite and nothing else. Only the one
		// immediately behind der-die-das should have seen those two cards; a deck
		// further down the chain is watching a deck that was not touched, and its
		// bar must stay at zero rather than inheriting progress from up the chain.
		want := 0
		if d.Requires == "der-die-das" {
			want = 2
		}

		if d.RequiresSeen != want {
			t.Errorf("deck %q reports %d seen in prerequisite %q, want %d",
				d.ID, d.RequiresSeen, d.Requires, want)
		}
	}
}

// The gate has to actually open, and the number it opens at has to be the one the
// locked card was promising all along.
func TestDecksUnlockOnceThePrerequisiteIsCovered(t *testing.T) {
	study := studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()})
	app := studyapp.New(studyapp.Config{
		Vocab:      vocabbus.NewBusiness(seeddb.New()),
		Study:      study,
		Curriculum: newCurriculum(t),
		BatchLimit: 5,
		Now:        fixedNow,
		Auth:       studyapp.SingleUser{},
	})
	h := app.Handler()

	locked := getDecks(t, h)[1]
	if locked.Unlocked {
		t.Fatal("the second deck starts unlocked")
	}

	// Study exactly as much of the noun deck as the locked card said was needed,
	// straight through the Business port — the point is the gate, not the HTTP.
	nouns, err := vocabbus.NewBusiness(seeddb.New()).Deck(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("loading the noun deck: %v", err)
	}

	for i := range locked.RequiresNeed {
		if _, err := study.Grade(context.Background(), userid.Local(), langcode.German,
			deckid.DerDieDas, studybus.GradeInput{
				Item:   nouns[i].Lemma,
				Rating: rating.Good,
				Role:   roleanswer.None,
			}, fixedNow()); err != nil {
			t.Fatalf("grading %q: %v", nouns[i].Lemma, err)
		}
	}

	after := getDecks(t, h)[1]
	if !after.Unlocked {
		t.Fatalf("deck %q is still locked after %d of %d items seen",
			after.ID, after.RequiresSeen, after.RequiresNeed)
	}
	if after.RequiresSeen != locked.RequiresNeed {
		t.Errorf("RequiresSeen = %d, want %d", after.RequiresSeen, locked.RequiresNeed)
	}

	// One deck at a time: covering the first must not open the third.
	if third := getDecks(t, h)[2]; third.Unlocked {
		t.Errorf("deck %q opened as well; only the deck behind der-die-das should have", third.ID)
	}
}

func TestDecksRejectsBadLanguage(t *testing.T) {
	rec := httptest.NewRecorder()
	newServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/decks?lang=XYZ", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBatchReturnsPreloadedCardsWithAnswers(t *testing.T) {
	h := newServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/batch?lang=de", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var body struct {
		Lang  string `json:"lang"`
		Cards []struct {
			Lemma     string `json:"lemma"`
			Article   string `json:"article"`
			Gloss     string `json:"gloss"`
			Example   string `json:"example"`
			ExampleEn string `json:"example_en"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}

	if body.Lang != "de" {
		t.Fatalf("lang = %q, want de", body.Lang)
	}
	if len(body.Cards) != 5 {
		t.Fatalf("cards = %d, want 5 (batch limit)", len(body.Cards))
	}
	for _, c := range body.Cards {
		if c.Lemma == "" || c.Gloss == "" {
			t.Fatalf("card missing lemma/gloss: %+v", c)
		}
		// The example ships with the batch rather than being fetched when the
		// round ends, so that the end-of-round review costs no round-trip. If it
		// stops arriving here, that review silently goes blank.
		if c.Example == "" || c.ExampleEn == "" {
			t.Fatalf("card %q missing example/translation: %+v", c.Lemma, c)
		}
		if !strings.Contains(c.Example, c.Lemma) {
			t.Fatalf("card %q example %q does not contain the noun", c.Lemma, c.Example)
		}
		switch c.Article {
		case "der", "die", "das":
		default:
			t.Fatalf("card %q has bad article %q", c.Lemma, c.Article)
		}
	}
}

func TestBatchRejectsBadLanguage(t *testing.T) {
	h := newServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/batch?lang=XYZ", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A batch with cards in it says nothing about the future: the learner has work in
// front of them, and the second read of their progress that next_due costs is not
// worth paying to tell them what happens after it.
func TestBatchOmitsNextDueWhileCardsRemain(t *testing.T) {
	h := newServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/batch?lang=de", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body struct {
		Cards   []json.RawMessage `json:"cards"`
		NextDue string            `json:"next_due"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}

	if len(body.Cards) == 0 {
		t.Fatal("expected a full batch from an untouched deck")
	}
	if body.NextDue != "" {
		t.Fatalf("next_due = %q, want it omitted while cards remain", body.NextDue)
	}
}

// The state a finished deck actually leaves a learner in: everything seen, nothing
// due, and an empty batch. That is the scheduler working, but it is also the one
// answer that gives the client nothing to say, so the batch has to carry when the
// deck reopens — and the shelf has to carry it too, or a caught-up deck reads as an
// empty one.
func TestExhaustedDeckReportsWhenTheNextCardIsDue(t *testing.T) {
	h := newServer(t)
	ctx := t.Context()

	nouns, err := vocabbus.NewBusiness(seeddb.New()).Deck(ctx, langcode.German)
	if err != nil {
		t.Fatalf("loading the deck: %v", err)
	}

	// Answer the whole deck, so nothing is unseen and nothing is due back yet.
	type outcome struct {
		Lemma  string `json:"lemma"`
		Rating string `json:"rating"`
	}
	payload := struct {
		Lang    string    `json:"lang"`
		Results []outcome `json:"results"`
	}{Lang: "de"}
	for _, n := range nouns {
		payload.Results = append(payload.Results, outcome{Lemma: n.Lemma, Rating: "easy"})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding flush: %v", err)
	}

	gradeReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/grade", bytes.NewReader(raw))
	gradeReq.Header.Set("Content-Type", "application/json")
	gradeRec := httptest.NewRecorder()
	h.ServeHTTP(gradeRec, gradeReq)

	if gradeRec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", gradeRec.Code, gradeRec.Body)
	}

	batchReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/batch?lang=de", nil)
	batchRec := httptest.NewRecorder()
	h.ServeHTTP(batchRec, batchReq)

	var batch struct {
		Cards   []json.RawMessage `json:"cards"`
		NextDue string            `json:"next_due"`
	}
	if err := json.Unmarshal(batchRec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}

	if len(batch.Cards) != 0 {
		t.Fatalf("cards = %d, want an empty batch once the deck is answered", len(batch.Cards))
	}

	nextDue, err := time.Parse(time.RFC3339, batch.NextDue)
	if err != nil {
		t.Fatalf("next_due = %q, want an RFC3339 instant: %v", batch.NextDue, err)
	}
	if !nextDue.After(fixedNow()) {
		t.Fatalf("next_due = %s, want a moment after now (%s)", nextDue, fixedNow())
	}

	// The same fact has to reach the shelf, which is where a learner decides what
	// to do next.
	decksReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/decks?lang=de", nil)
	decksRec := httptest.NewRecorder()
	h.ServeHTTP(decksRec, decksReq)

	var shelf struct {
		Decks []struct {
			ID      string `json:"id"`
			DueNow  int    `json:"due_now"`
			NextDue string `json:"next_due"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(decksRec.Body.Bytes(), &shelf); err != nil {
		t.Fatalf("decoding shelf: %v", err)
	}

	var found bool
	for _, d := range shelf.Decks {
		if d.ID != deckid.DerDieDas.String() {
			continue
		}
		found = true

		if d.DueNow != 0 {
			t.Fatalf("due_now = %d, want 0 for a deck just answered in full", d.DueNow)
		}
		if d.NextDue != batch.NextDue {
			t.Fatalf("shelf next_due = %q, batch next_due = %q; they describe the same card and must agree", d.NextDue, batch.NextDue)
		}
	}

	if !found {
		t.Fatalf("no %q deck on the shelf", deckid.DerDieDas)
	}
}

// A deck nobody has touched has nothing scheduled: every card is available rather
// than waiting, so there is no "next" review to name and the field stays absent.
// Without this, a zero time formatted as year 1 would read as a review due in the
// distant past.
func TestUntouchedDeckHasNoNextDue(t *testing.T) {
	h := newServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/summary?lang=de", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var sum struct {
		Learned int    `json:"learned"`
		NextDue string `json:"next_due"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decoding summary: %v", err)
	}

	if sum.Learned != 0 {
		t.Fatalf("learned = %d, want an untouched deck", sum.Learned)
	}
	if sum.NextDue != "" {
		t.Fatalf("next_due = %q, want it absent when nothing is scheduled", sum.NextDue)
	}
}

func TestGradeFlushAppliesAndReportsProgress(t *testing.T) {
	h := newServer(t)
	ctx := context.Background()

	// Pull a batch so we know real lemmas to grade.
	batchReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/batch?lang=de", nil)
	batchRec := httptest.NewRecorder()
	h.ServeHTTP(batchRec, batchReq)

	var batch struct {
		Cards []struct {
			Lemma string `json:"lemma"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(batchRec.Body.Bytes(), &batch); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}
	if len(batch.Cards) == 0 {
		t.Fatal("no cards to grade")
	}

	// Flush grades for the whole batch: everything answered "good".
	type outcome struct {
		Lemma  string `json:"lemma"`
		Rating string `json:"rating"`
	}
	payload := struct {
		Lang    string    `json:"lang"`
		Results []outcome `json:"results"`
	}{Lang: "de"}
	for _, c := range batch.Cards {
		payload.Results = append(payload.Results, outcome{Lemma: c.Lemma, Rating: "good"})
	}
	raw, _ := json.Marshal(payload)

	gradeReq := httptest.NewRequest(http.MethodPost, "/api/grade", bytes.NewReader(raw))
	gradeReq.Header.Set("Content-Type", "application/json")
	gradeRec := httptest.NewRecorder()
	h.ServeHTTP(gradeRec, gradeReq)

	if gradeRec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", gradeRec.Code, gradeRec.Body)
	}

	var gr struct {
		Applied  int `json:"applied"`
		Learned  int `json:"learned"`
		DeckSize int `json:"deck_size"`
	}
	if err := json.Unmarshal(gradeRec.Body.Bytes(), &gr); err != nil {
		t.Fatalf("decoding grade response: %v", err)
	}
	if gr.Applied != len(batch.Cards) {
		t.Fatalf("applied = %d, want %d", gr.Applied, len(batch.Cards))
	}
	if gr.Learned != len(batch.Cards) {
		t.Fatalf("learned = %d, want %d", gr.Learned, len(batch.Cards))
	}
	if gr.DeckSize < 200 {
		t.Fatalf("deck_size = %d, want the full deck", gr.DeckSize)
	}
}

// studyDeck answers every card the named deck offers, batch after batch, until it
// runs dry. It returns how many distinct cards were answered — which is the deck's
// coverage, and therefore what the next deck's gate is measured against.
func studyDeck(t *testing.T, h http.Handler, deck string) int {
	t.Helper()

	seen := map[string]bool{}

	for round := range 100 {
		batchRec := httptest.NewRecorder()
		h.ServeHTTP(batchRec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/batch?lang=de&deck="+deck, nil))

		if batchRec.Code != http.StatusOK {
			t.Fatalf("batch %d of %q: status %d; body=%s", round, deck, batchRec.Code, batchRec.Body)
		}

		var batch struct {
			Cards []struct {
				Item string `json:"item"`
			} `json:"cards"`
		}
		if err := json.Unmarshal(batchRec.Body.Bytes(), &batch); err != nil {
			t.Fatalf("decoding batch: %v", err)
		}

		if len(batch.Cards) == 0 {
			return len(seen)
		}

		type outcome struct {
			Item   string `json:"item"`
			Rating string `json:"rating"`
		}
		payload := struct {
			Lang    string    `json:"lang"`
			Deck    string    `json:"deck"`
			Results []outcome `json:"results"`
		}{Lang: "de", Deck: deck}

		for _, c := range batch.Cards {
			seen[c.Item] = true
			payload.Results = append(payload.Results, outcome{Item: c.Item, Rating: "easy"})
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encoding flush: %v", err)
		}

		gradeRec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/grade", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(gradeRec, req)

		if gradeRec.Code != http.StatusOK {
			t.Fatalf("grade in %q: status %d; body=%s", deck, gradeRec.Code, gradeRec.Body)
		}
	}

	t.Fatalf("deck %q never ran dry", deck)

	return 0
}

// unlockPrepositions works through the noun deck, which is what the preposition
// deck is gated behind. Every test that wants to reach a case deck has to pay this
// first — which is the course working, not an obstacle: the second deck is not
// supposed to be reachable from a standing start.
func unlockPrepositions(t *testing.T, h http.Handler) {
	t.Helper()

	if seen := studyDeck(t, h, "der-die-das"); seen == 0 {
		t.Fatal("studied no nouns")
	}
}

// The preposition deck is a different drill over different material, and it has to
// arrive as real cards — trigger, case and the declined phrase that teaches it —
// through the same endpoint that serves the nouns.
func TestBatchServesTheCaseDeck(t *testing.T) {
	h := newServer(t)
	unlockPrepositions(t, h)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/batch?lang=de&deck=de-praepositionen", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var body struct {
		Deck  string `json:"deck"`
		Cards []struct {
			Item    string `json:"item"`
			Answer  string `json:"answer"`
			Phrase  string `json:"phrase"`
			Gloss   string `json:"gloss"`
			Lemma   string `json:"lemma"`
			Article string `json:"article"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}

	if body.Deck != "de-praepositionen" {
		t.Fatalf("deck = %q, want de-praepositionen", body.Deck)
	}
	if len(body.Cards) == 0 {
		t.Fatal("no cards from the preposition deck")
	}

	for _, c := range body.Cards {
		switch c.Answer {
		case "akkusativ", "dativ", "genitiv":
		default:
			t.Fatalf("card %q has answer %q, want a governed case", c.Item, c.Answer)
		}

		if c.Phrase == "" || c.Gloss == "" {
			t.Fatalf("card %q is missing its phrase or gloss: %+v", c.Item, c)
		}
		if !strings.Contains(c.Phrase, c.Item) {
			// Pattern triggers are allowed to fail this in the store; none of them
			// are in the preposition deck.
			t.Fatalf("card %q phrase %q does not show the trigger", c.Item, c.Phrase)
		}

		// A case card has no gender and no lemma. Inventing either to fill the
		// deprecated fields would hand a stale client something false to draw.
		if c.Lemma != "" || c.Article != "" {
			t.Fatalf("case card %q carries noun-deck fields: lemma=%q article=%q", c.Item, c.Lemma, c.Article)
		}
	}
}

// The lock has to hold at the endpoint, not only in the shelf that draws it.
// Otherwise the deck is open to anyone who types its id into the URL, and the
// progress they build there is exactly what unlocks it for real.
func TestBatchRefusesALockedDeck(t *testing.T) {
	h := newServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/batch?lang=de&deck=de-verben-kasus", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}

	var body struct {
		Error        string `json:"error"`
		Requires     string `json:"requires"`
		RequiresNeed int    `json:"requires_need"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding refusal: %v", err)
	}

	if body.Requires != "de-praepositionen" {
		t.Fatalf("requires = %q, want the preposition deck", body.Requires)
	}
	if body.RequiresNeed == 0 {
		t.Fatal("refusal should say how many cards are needed")
	}
}

// Refusing the batch but not the grade would leave the gate walkable: a client can
// post grades for cards it never fetched, and progress in a locked deck is what
// unlocks its successor.
func TestGradeRefusesALockedDeck(t *testing.T) {
	h := newServer(t)

	body := `{"lang":"de","deck":"de-verben-kasus","results":[{"item":"helfen","rating":"good"}]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/grade", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

// The whole point of the course: working through one deck opens the next. This is
// the path that was impossible before the case drill existed — the preposition deck
// had no way to be studied, so the verb deck could never be reached.
func TestWorkingThroughAPrerequisiteUnlocksTheNextDeck(t *testing.T) {
	h := newServer(t)
	unlockPrepositions(t, h)

	locked := httptest.NewRecorder()
	h.ServeHTTP(locked, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/batch?lang=de&deck=de-verben-kasus", nil))
	if locked.Code != http.StatusForbidden {
		t.Fatalf("verb deck should start locked, got %d", locked.Code)
	}

	seen := studyDeck(t, h, "de-praepositionen")
	if seen == 0 {
		t.Fatal("studied no preposition cards")
	}

	opened := httptest.NewRecorder()
	h.ServeHTTP(opened, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/batch?lang=de&deck=de-verben-kasus", nil))
	if opened.Code != http.StatusOK {
		t.Fatalf("verb deck still locked after %d preposition cards: status %d; body=%s",
			seen, opened.Code, opened.Body)
	}

	var batch struct {
		Cards []struct {
			Item   string `json:"item"`
			Answer string `json:"answer"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(opened.Body.Bytes(), &batch); err != nil {
		t.Fatalf("decoding batch: %v", err)
	}
	if len(batch.Cards) == 0 {
		t.Fatal("verb deck unlocked but served no cards")
	}

	// The shelf must agree with the endpoint; they are what a learner sees and
	// what they get.
	shelfRec := httptest.NewRecorder()
	h.ServeHTTP(shelfRec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/decks?lang=de", nil))

	var shelf struct {
		Decks []struct {
			ID       string `json:"id"`
			Unlocked bool   `json:"unlocked"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(shelfRec.Body.Bytes(), &shelf); err != nil {
		t.Fatalf("decoding shelf: %v", err)
	}

	for _, d := range shelf.Decks {
		if d.ID == "de-verben-kasus" && !d.Unlocked {
			t.Fatal("endpoint serves the verb deck but the shelf still shows it locked")
		}
	}
}

// A cached client predates both the deck parameter and the item field: it posts a
// flush naming neither, with `lemma` as the card key. The decoder rejects unknown
// fields, so if either name stopped being accepted that client would 400 on every
// flush and lose the round the learner had just finished.
func TestGradeStillAcceptsTheOldLemmaWireFormat(t *testing.T) {
	h := newServer(t)

	body := `{"lang":"de","results":[{"lemma":"Haus","rating":"good"}]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/grade", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var gr struct {
		Applied int    `json:"applied"`
		Deck    string `json:"deck"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &gr); err != nil {
		t.Fatalf("decoding grade response: %v", err)
	}
	if gr.Applied != 1 {
		t.Fatalf("applied = %d, want 1", gr.Applied)
	}
}

// Both names for one key is a client that has lost track of which field it owns.
// Preferring one silently would file a grade against whichever card happened to
// win, so it is refused.
func TestGradeRejectsBothItemAndLemma(t *testing.T) {
	h := newServer(t)

	body := `{"lang":"de","results":[{"item":"Haus","lemma":"Haus","rating":"good"}]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/grade", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

func TestBatchRejectsAnUnknownDeck(t *testing.T) {
	h := newServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/batch?lang=de&deck=de-does-not-exist", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

// Grades must land in the deck they were earned in. If the deck were ignored, a
// preposition answered correctly would advance a noun card of the same name, and
// two decks would quietly share one schedule.
func TestGradesAreScopedToTheirDeck(t *testing.T) {
	h := newServer(t)
	unlockPrepositions(t, h)

	nounsBefore := learnedIn(t, h, "der-die-das")

	body := `{"lang":"de","deck":"de-praepositionen","results":[{"item":"durch","rating":"good"}]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/grade", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var gr struct {
		Learned  int `json:"learned"`
		DeckSize int `json:"deck_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &gr); err != nil {
		t.Fatalf("decoding grade response: %v", err)
	}
	if gr.Learned != 1 {
		t.Fatalf("learned = %d in the preposition deck, want 1", gr.Learned)
	}
	if gr.DeckSize != 22 {
		t.Fatalf("deck_size = %d, want the 22 prepositions", gr.DeckSize)
	}

	// The noun deck must be untouched by that flush.
	if after := learnedIn(t, h, "der-die-das"); after != nounsBefore {
		t.Fatalf("noun deck learned went %d -> %d after grading a preposition", nounsBefore, after)
	}
}

// learnedIn reports how many cards of one deck the learner has ever answered.
func learnedIn(t *testing.T, h http.Handler, deck string) int {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/summary?lang=de&deck="+deck, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("summary for %q: status %d; body=%s", deck, rec.Code, rec.Body)
	}

	var sum struct {
		Learned int `json:"learned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decoding summary: %v", err)
	}

	return sum.Learned
}

func TestGradeRejectsUnknownRating(t *testing.T) {
	h := newServer(t)

	body := `{"lang":"de","results":[{"lemma":"Haus","rating":"perfect"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/grade", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a bad rating", rec.Code)
	}
}

func TestServesEmbeddedClientWhenConfigured(t *testing.T) {
	vocab := vocabbus.NewBusiness(seeddb.New())
	study := studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()})
	app := studyapp.New(studyapp.Config{
		Vocab:  vocab,
		Study:  study,
		Now:    fixedNow,
		Auth:   studyapp.SingleUser{},
		Static: studyapp.Assets(),
	})
	h := app.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Übung")) {
		t.Fatal("index.html did not render the app title")
	}
}

// Every study route must refuse an unauthenticated request. This is the whole
// point of the accounts work: before it, any caller wrote to one shared deck.
//
// A nil Auth stands in for "authentication was not wired". It must close the app
// rather than open it — forgetting to pass an Authenticator is exactly the
// mistake that would otherwise ship a public, world-writable deck.
func TestStudyRoutesRequireASignedInLearner(t *testing.T) {
	app := studyapp.New(studyapp.Config{
		Vocab:      vocabbus.NewBusiness(seeddb.New()),
		Study:      studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()}),
		Curriculum: newCurriculum(t),
		Now:        fixedNow,
		// Auth deliberately omitted.
	})
	h := app.Handler()

	tests := []struct {
		method, path, body string
	}{
		// The catalog is per-learner — it reports progress and what is unlocked —
		// so it is a study route and not public.
		{http.MethodGet, "/api/decks?lang=de", ""},
		{http.MethodGet, "/api/batch?lang=de", ""},
		{http.MethodGet, "/api/summary?lang=de", ""},
		{http.MethodPost, "/api/grade", `{"lang":"de","results":[{"lemma":"Mann","rating":"good"}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body *bytes.Reader
			if tc.body != "" {
				body = bytes.NewReader([]byte(tc.body))
			} else {
				body = bytes.NewReader(nil)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, body))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// getConfusion fetches a deck's error profile.
func getConfusion(t *testing.T, h http.Handler, deck string) confusionBody {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/confusion?lang=de&deck="+deck, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("confusion status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var got confusionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding confusion: %v", err)
	}

	return got
}

type confusionBody struct {
	Deck     string   `json:"deck"`
	Answers  []string `json:"answers"`
	Cells    [][]int  `json:"cells"`
	Recorded int      `json:"recorded"`
}

// cell reads the matrix by answer name rather than by index, so a test says what
// it means and does not silently pass if the two axes are ever transposed.
func (c confusionBody) cell(t *testing.T, wanted, said string) int {
	t.Helper()

	row := slices.Index(c.Answers, wanted)
	col := slices.Index(c.Answers, said)
	if row < 0 || col < 0 {
		t.Fatalf("answers %v have no cell for wanted=%q said=%q", c.Answers, wanted, said)
	}

	return c.Cells[row][col]
}

func gradeRaw(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/grade", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// The whole point of recording the answer: a learner who calls "Frau" masculine
// and one who calls it neuter have made different mistakes, and the profile has to
// tell them apart. "Mann" answered correctly lands on the diagonal.
func TestConfusionProfileSeparatesTheTwoWaysOfBeingWrong(t *testing.T) {
	h := newServer(t)

	rec := gradeRaw(t, h, `{"lang":"de","deck":"der-die-das","results":[
		{"item":"Frau","rating":"again","given":"der","answer_ms":2100},
		{"item":"Mann","rating":"good","given":"der","answer_ms":800}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	got := getConfusion(t, h, "der-die-das")

	if got.Recorded != 2 {
		t.Errorf("recorded = %d, want 2", got.Recorded)
	}
	if n := got.cell(t, "die", "der"); n != 1 {
		t.Errorf("die answered der = %d, want 1", n)
	}
	if n := got.cell(t, "die", "das"); n != 0 {
		t.Errorf("die answered das = %d, want 0 -- the two mistakes are being conflated", n)
	}
	if n := got.cell(t, "der", "der"); n != 1 {
		t.Errorf("der answered der = %d, want 1 -- the diagonal is what turns counts into a rate", n)
	}
}

// The log only ever grows, so an answer the deck does not offer would sit in a
// learner's profile for good. It has to be refused at the door, and refusing it
// must not half-apply the flush.
func TestGradeRejectsAnAnswerTheDeckDoesNotOffer(t *testing.T) {
	h := newServer(t)

	rec := gradeRaw(t, h, `{"lang":"de","deck":"der-die-das","results":[
		{"item":"Mann","rating":"good","given":"dativ"}
	]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grade status = %d, want 400; body=%s", rec.Code, rec.Body)
	}

	if decks := getDecks(t, h)[0]; decks.Learned != 0 {
		t.Errorf("a refused flush still wrote %d cards", decks.Learned)
	}
}

// A browser holding a client from before this release sends neither field. Those
// grades must still apply -- refusing them would cost that learner the round they
// had just finished -- and must contribute nothing to the profile rather than an
// answer nobody gave.
func TestGradeWithoutAnAnswerStillApplies(t *testing.T) {
	h := newServer(t)

	rec := gradeRaw(t, h, `{"lang":"de","results":[{"lemma":"Mann","rating":"good"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	if learned := getDecks(t, h)[0].Learned; learned != 1 {
		t.Fatalf("learned = %d, want 1", learned)
	}

	got := getConfusion(t, h, "der-die-das")
	if got.Recorded != 0 {
		t.Errorf("recorded = %d, want 0 -- an unreported answer was invented", got.Recorded)
	}
}

// A negative answer time is a broken clock, not a slow learner, and it would skew
// every average taken over the column later.
func TestGradeRejectsANegativeAnswerTime(t *testing.T) {
	h := newServer(t)

	rec := gradeRaw(t, h, `{"lang":"de","results":[{"item":"Mann","rating":"good","answer_ms":-5}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grade status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

// The grid is always the deck's full answer set, even before anything is studied.
// A client draws the axes from this response alone, so an untouched deck has to
// answer with a complete empty matrix rather than nothing to draw.
func TestConfusionOfAnUntouchedDeckIsAFullEmptyGrid(t *testing.T) {
	got := getConfusion(t, newServer(t), "der-die-das")

	if got.Recorded != 0 {
		t.Errorf("recorded = %d, want 0", got.Recorded)
	}
	if len(got.Answers) != 3 {
		t.Fatalf("answers = %v, want three", got.Answers)
	}
	if len(got.Cells) != len(got.Answers) {
		t.Fatalf("cells has %d rows for %d answers", len(got.Cells), len(got.Answers))
	}
	for i, row := range got.Cells {
		if len(row) != len(got.Answers) {
			t.Fatalf("row %d has %d cells for %d answers", i, len(row), len(got.Answers))
		}
	}
}

// A profile is per deck. Answers given in the noun deck must not appear in the
// preposition deck's grid, which is the same scoping every other study route has.
func TestConfusionIsScopedToItsDeck(t *testing.T) {
	h := newServer(t)

	rec := gradeRaw(t, h, `{"lang":"de","deck":"der-die-das","results":[
		{"item":"Mann","rating":"good","given":"der"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("grade status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	if got := getConfusion(t, h, "de-praepositionen"); got.Recorded != 0 {
		t.Errorf("the noun deck's answers leaked into the preposition deck: %+v", got)
	}
}
