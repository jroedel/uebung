package triggerbus

import (
	"github.com/jroedel/uebung/business/types/gcase"
	"github.com/jroedel/uebung/business/types/langcode"
)

// Trigger is one card in a case deck: a word that governs a case, and the case it
// governs.
//
// The word is called a trigger rather than a preposition or a verb because both
// decks are the same drill. "durch" takes the accusative and "helfen" takes the
// dative for entirely different grammatical reasons, but the question put to a
// learner is identical — here is a word, which case does it want — and a type
// that named one of the two would need a sibling that differed only in its field
// names.
//
// Trigger doubles as the card's identity within a deck, exactly as a noun's lemma
// does in the vocab deck: the authored decks have no repeats, so the word is a
// natural key and a study record can point back at a card by (deck, trigger). The
// deck is what keeps the namespaces apart, so a word appearing in both the
// preposition and the verb deck would be two cards and not one.
type Trigger struct {
	Word string     // the trigger itself, e.g. "durch"; unique within a deck.
	Case gcase.Case // the case it governs — the answer being drilled.

	Gloss string // short English meaning, for feedback only; never graded.

	// Phrase is the trigger in a minimal declined example — "durch den Park" —
	// and is the part that actually teaches: it shows the case as a form rather
	// than as a label, which is how the pattern is recognised in the wild.
	Phrase string

	Example   string // full sentence using the trigger, for post-round review.
	ExampleEn string // English translation of Example.

	// Note is an optional authored aside for a card that needs one — a trigger
	// with a second, rarer case, or one whose phrase looks like a counterexample.
	// Empty for the great majority of cards.
	Note string

	Lang langcode.LangCode
}
