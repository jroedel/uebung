package filedb_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/stores/filedb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
)

func sampleCard() studybus.Progress {
	return studybus.Progress{
		User:       userid.Local(),
		Lang:       langcode.German,
		Lemma:      "Haus",
		Stability:  4.2,
		Difficulty: 5.1,
		State:      cardstate.Review,
		Reps:       3,
		Lapses:     1,
		LastReview: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Due:        time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC),
	}
}

func TestSavePersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cards.json")

	s1, err := filedb.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := sampleCard()
	if err := s1.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reopen from disk: the card must come back byte-for-byte in its strong form.
	s2, err := filedb.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := s2.Get(ctx, want.User, want.Lang, want.Lemma)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("card missing after reopen")
	}
	if got != want {
		t.Fatalf("round-trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestGetMissingCardIsNotAnError(t *testing.T) {
	ctx := context.Background()
	s, err := filedb.Open(filepath.Join(t.TempDir(), "cards.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, ok, err := s.Get(ctx, userid.Local(), langcode.German, "Nichtvorhanden")
	if err != nil {
		t.Fatalf("Get on missing card errored: %v", err)
	}
	if ok {
		t.Fatal("missing card reported as found")
	}
}

func TestListFiltersByUserAndLang(t *testing.T) {
	ctx := context.Background()
	s, err := filedb.Open(filepath.Join(t.TempDir(), "cards.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	c := sampleCard()
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	other := sampleCard()
	other.User = userid.MustParse("someone-else")
	other.Lemma = "Zeit"
	if err := s.Save(ctx, other); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	got, err := s.List(ctx, userid.Local(), langcode.German)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Lemma != "Haus" {
		t.Fatalf("List = %+v, want only Local's Haus card", got)
	}
}
