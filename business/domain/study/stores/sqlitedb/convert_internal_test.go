package sqlitedb

import (
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
)

// The review log is written now and read later, by a scoring feature that does not
// exist yet. That gap is exactly where a converter pair rots unnoticed, so the
// round-trip is tested at the point the writing side is built rather than at the
// point somebody first needs the reading side.
func TestReviewRoundTripsThroughTheStorageEdge(t *testing.T) {
	want := studybus.Review{
		User:   userid.Local(),
		Lang:   langcode.German,
		Deck:   deckid.DerDieDas,
		Item:   "Haus",
		Rating: rating.Hard,
		Role:   roleanswer.Incorrect,
		At:     time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC),
	}

	got, err := toBusReview(toDBReview(want))
	if err != nil {
		t.Fatalf("toBusReview: %v", err)
	}

	if got.User != want.User || got.Lang != want.Lang || got.Deck != want.Deck || got.Item != want.Item {
		t.Errorf("identity: got %+v, want %+v", got, want)
	}
	if got.Rating != want.Rating {
		t.Errorf("rating: got %v, want %v", got.Rating, want.Rating)
	}
	if got.Role != want.Role {
		t.Errorf("role: got %v, want %v", got.Role, want.Role)
	}
	if !got.At.Equal(want.At) {
		t.Errorf("time: got %v, want %v", got.At, want.At)
	}
}

// roleanswer.None is what every deck records today, and it is the zero value —
// which is exactly the value most likely to be dropped silently by a converter and
// read back as something else.
func TestNoneRoleRoundTrips(t *testing.T) {
	rev := studybus.Review{
		User:   userid.Local(),
		Lang:   langcode.German,
		Deck:   deckid.DerDieDas,
		Item:   "Zeit",
		Rating: rating.Good,
		Role:   roleanswer.None,
		At:     time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC),
	}

	row := toDBReview(rev)
	if row.RoleAnswer != "none" {
		t.Errorf("stored role_answer = %q, want %q", row.RoleAnswer, "none")
	}

	got, err := toBusReview(row)
	if err != nil {
		t.Fatalf("toBusReview: %v", err)
	}
	if got.Role != roleanswer.None {
		t.Errorf("role came back as %v, want none", got.Role)
	}
}
