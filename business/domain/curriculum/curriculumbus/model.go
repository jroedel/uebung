package curriculumbus

import (
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/drillkind"
	"github.com/jroedel/uebung/business/types/langcode"
)

// Deck is one entry in the catalog: everything needed to put a deck on a shelf
// and let someone decide whether to pick it up.
//
// It deliberately does not hold the deck's items. What a learner is asked is a
// separate concern from what the shelf looks like, and keeping them apart is what
// lets the picker be built and shipped before a drill exists to answer. The noun
// deck's items live in the vocab domain and have since the first commit; the
// trigger decks' items sit beside their catalog entry in the same seed file and
// get a domain of their own when the drill that reads them is written.
type Deck struct {
	ID       deckid.DeckID
	Lang     langcode.LangCode
	Title    string
	Subtitle string
	Drill    drillkind.DrillKind

	// Answers is the closed set of replies this deck accepts, in the order a
	// client should offer them — ["der", "die", "das"] for the noun deck. It is
	// primitive rather than a strong type because its meaning changes with the
	// drill: the same three-button interaction asks for a gender in one deck and a
	// case in another, and a type that had to cover both would say nothing.
	Answers []string

	// Prerequisite is the deck that gates this one, or the zero DeckID when
	// nothing does. Exactly one, not a set: a chain is something a learner can
	// hold in their head and a graph is not.
	Prerequisite deckid.DeckID

	// Intro is the orienting explanation shown before the first session — the
	// pattern behind the deck rather than a rule to memorise. See Intro.
	Intro Intro

	// Size is how many items the deck holds, or 0 when the catalog does not carry
	// them. Zero is not "an empty deck": it means the items live in another
	// domain, and the App layer resolves the count from there. Only the noun deck
	// is in that position today.
	Size int
}

// Intro is what a learner reads the first time they open a deck.
//
// It is structured rather than a blob of prose because it is doing a specific
// job: giving an orienting scheme before the drilling starts, so that practice
// has something to attach to instead of being pure association. Heading and Body
// state the idea, Groups lay the material out as the small number of blocks it
// actually forms, and Closing says what to do when stuck — which is the part a
// learner will reach for mid-round.
type Intro struct {
	Heading string
	Body    []string
	Groups  []IntroGroup
	Closing string
}

// IntroGroup is one block of the scheme: the answer it leads to, a label for it,
// the members worth naming, and the hook that makes the block memorable.
//
// Answer names one of the deck's Answers, so the client can colour a group the
// same way it colours that button and the intro and the drill look like one
// thing. It is empty for a group that does not map to a single answer.
type IntroGroup struct {
	Answer  string
	Label   string
	Members string
	Hook    string
}

// Coverage is how far a learner has got through one deck, as the unlock rule
// measures it.
//
// Seen counts items answered at least once, not items retained. That is a
// deliberately modest measure and it is chosen for one property: it only ever
// goes up. A gate built on retention would re-lock a deck the day a learner
// lapsed a few cards in the deck before it, which is the single worst thing an
// unlock rule can do. Coverage cannot do that.
//
// The honest reading of the rule is therefore "you have worked through enough of
// the previous deck to be ready for this one", not "you have mastered it". A
// retention-based gate becomes possible once the review log has a reader, and the
// shape here is what it would slot into.
type Coverage struct {
	Size int
	Seen int
}

// Gate is the verdict on whether a deck is open to a learner, with the numbers
// behind it so a locked deck can say what it is waiting for rather than just
// being closed.
type Gate struct {
	// Requires is the deck that gates this one, zero when nothing does.
	Requires deckid.DeckID

	// Met is whether the deck is open. Always true when Requires is zero.
	Met bool

	// Seen and Need are progress through the prerequisite: how many of its items
	// the learner has answered, and how many are needed. Both 0 when nothing
	// gates the deck.
	Seen int
	Need int
}
