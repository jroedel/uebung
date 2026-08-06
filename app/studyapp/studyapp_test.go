package studyapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			Lemma   string `json:"lemma"`
			Article string `json:"article"`
			Gloss   string `json:"gloss"`
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
