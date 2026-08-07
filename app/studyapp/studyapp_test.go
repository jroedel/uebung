package studyapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jroedel/uebung/app/studyapp"
	"github.com/jroedel/uebung/business/domain/study/stores/memdb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/vocab/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/foundation/fsrs"
)

// fixedNow pins the app's clock so scheduling is deterministic in tests.
func fixedNow() time.Time { return time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC) }

func newServer(t *testing.T) http.Handler {
	t.Helper()
	vocab := vocabbus.NewBusiness(seeddb.New())
	study := studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()})
	app := studyapp.New(studyapp.Config{
		Vocab:      vocab,
		Study:      study,
		BatchLimit: 5,
		Now:        fixedNow,
	})
	return app.Handler()
}

// newServerWithStatic is newServer plus the embedded client, for the routes that
// only exist when assets are served.
func newServerWithStatic(t *testing.T) http.Handler {
	t.Helper()

	app := studyapp.New(studyapp.Config{
		Vocab:  vocabbus.NewBusiness(seeddb.New()),
		Study:  studybus.NewBusiness(memdb.New(), studybus.Config{FSRS: fsrs.Default()}),
		Now:    fixedNow,
		Static: studyapp.Assets(),
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
