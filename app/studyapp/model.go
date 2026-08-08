package studyapp

// This file holds the App-layer wire types and their converters. Every field
// here is a primitive — string, int, bool — because this is the API edge, where
// JSON is spoken and no business/types strong type is allowed to appear. Parsing
// and validation of incoming primitives happens in the toBus* converters and
// nowhere else; outgoing strong types are flattened explicitly in the fromBus*
// builders, never by leaning on a strong type's own JSON marshaling.

// batchCardResponse is one question as the client preloads it. The correct answer
// travels with the card on purpose: this is self-paced practice, not a graded
// exam, so the browser can reveal the answer and grade the swipe with zero
// round-trips. Gloss is shown as feedback once the learner has answered.
//
// Example and ExampleEn ride along for the same reason. They are only needed for
// the cards a learner gets wrong, which is not known until the round is over —
// but fetching them then would put a network round-trip between the last swipe
// and the result panel. Shipping them with the batch keeps the whole round local.
//
// Item and Answer are named for what they are rather than for the deck that came
// first: a card's key is a noun's lemma in one deck and a preposition in another,
// and its answer is a gender in one and a case in another. The drill on the deck
// says which, and the client already branches on it.
type batchCardResponse struct {
	Item      string `json:"item"`   // the card's key within its deck.
	Answer    string `json:"answer"` // the correct reply: an article, or a case.
	Gloss     string `json:"gloss"`
	Example   string `json:"example"`
	ExampleEn string `json:"example_en"`

	// Phrase shows a case deck's trigger already declined — "durch den Park" —
	// which is the part that teaches the pattern rather than naming it. Empty for
	// the noun deck, which has nothing of the kind.
	Phrase string `json:"phrase,omitempty"`

	// Note is an authored aside for the occasional card that needs one. Empty for
	// most.
	Note string `json:"note,omitempty"`

	// Lemma and Article repeat Item and Answer for the noun deck alone.
	//
	// They are here for a browser holding a cached app.js from before decks were
	// named on the wire: that client reads card.lemma and card.article, and would
	// render "undefined" against every card without them. They are set only for
	// the article drill — a case card has no lemma and no article, and inventing
	// one to fill the field would be worse than leaving a stale client to fail on
	// a deck it cannot draw anyway.
	//
	// Deprecated: read Item and Answer. Removable once no cached client predates
	// this release.
	Lemma   string `json:"lemma,omitempty"`
	Article string `json:"article,omitempty"`
}

// batchResponse is a whole preloaded study session: the ordered questions plus
// the language they belong to, so the client can echo it back on the flush.
//
// NextDue rides along for the one case the client cannot otherwise explain: an
// empty batch. "Nothing to do" is the scheduler working, not an error, but a
// learner told only that is left guessing whether to wait ten minutes or a week —
// and the honest answer is already known here. It is set only when Cards is
// empty, because a learner with cards in front of them is not asking when the
// next one arrives, and computing it costs a second read of their progress.
type batchResponse struct {
	Lang    string              `json:"lang"`
	Deck    string              `json:"deck"` // which deck these cards came from.
	Cards   []batchCardResponse `json:"cards"`
	NextDue string              `json:"next_due,omitempty"` // RFC3339; see above.
}

// gradeRequest is the batched flush the client sends after working through the
// preloaded cards. Results are graded client-side (correct/incorrect, adjusted
// by answer speed) into an FSRS rating name, and the whole set posts at once so
// each swipe stayed local and instant.
type gradeRequest struct {
	Lang string `json:"lang"`

	// Deck names which deck was studied. Empty means the noun deck: a cached
	// client from before the course had more than one deck does not send the
	// field, and defaulting is what keeps its flushes landing in the right place
	// instead of failing the strict decode.
	Deck string `json:"deck"`

	Results []gradeOutcome `json:"results"`
}

// gradeOutcome is one graded card in a flush: which card, and how it went.
//
// Item and Lemma are the same field under two names. Lemma is what the wire
// called it when every card was a noun, and it is still accepted because the
// request decoder rejects unknown fields — a browser holding a cached app.js
// would have every flush 400 the moment the name changed, losing the round the
// learner had just finished. Exactly one of the two must be set; sending both is
// an error rather than a silent preference, since a client that disagrees with
// itself about a card's key is a client whose grades cannot be trusted.
//
// Deprecated: send Item. Lemma is removable once no cached client predates the
// release that introduced Item.
type gradeOutcome struct {
	Item   string `json:"item"`
	Lemma  string `json:"lemma"`
	Rating string `json:"rating"` // one of: again, hard, good, easy.
}

// gradeResponse reports the state of the deck after a flush was applied, so the
// client can show progress and decide whether to request another batch.
type gradeResponse struct {
	Applied  int    `json:"applied"`            // cards successfully graded.
	DueNow   int    `json:"due_now"`            // reviews already due again.
	Learned  int    `json:"learned"`            // cards ever reviewed at least once.
	DeckSize int    `json:"deck_size"`          // total cards in the deck.
	NextDue  string `json:"next_due,omitempty"` // RFC3339; see summaryResponse.
}

// summaryResponse is a lightweight progress snapshot for the language.
//
// NextDue is when the soonest-scheduled card comes back, RFC3339, empty when
// nothing is waiting — either because the deck has cards ready now or because it
// has never been touched. DueNow answers "is there anything to do"; NextDue
// answers "when will there be", and a caught-up deck needs the second one.
type summaryResponse struct {
	Lang     string `json:"lang"`
	Deck     string `json:"deck"`
	DeckSize int    `json:"deck_size"`
	Learned  int    `json:"learned"`
	DueNow   int    `json:"due_now"`
	NextDue  string `json:"next_due,omitempty"`
}

// catalogResponse is the shelf: every deck in a language, in course order, each
// carrying the learner's standing in it.
type catalogResponse struct {
	Lang  string         `json:"lang"`
	Decks []deckResponse `json:"decks"`
}

// deckResponse is one deck on the shelf.
//
// It carries the whole introduction rather than a link to it. The intro is a
// couple of kilobytes of authored text that never changes for a given build, and
// the client needs it at two different moments — before a learner's first session,
// and again whenever they choose to reread it. Shipping it with the catalog makes
// both instant and costs one request instead of two.
//
// Playability is deliberately not a field here. Whether a deck can be rendered is
// a fact about the client, not about the deck: a browser that has not learned a
// drill yet decides that from Drill, and a client that has will not need to be
// told. Sending a "playable" flag would put the client's own capabilities in the
// server's mouth and go stale the moment either side shipped without the other.
type deckResponse struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Drill    string   `json:"drill"`
	Answers  []string `json:"answers"`

	DeckSize int `json:"deck_size"` // items in the deck.
	Learned  int `json:"learned"`   // items answered at least once.
	DueNow   int `json:"due_now"`   // items due for review right now.

	// NextDue is when this deck's soonest-scheduled card returns, RFC3339, empty
	// when nothing is waiting. It is what lets a finished deck say "next review
	// tomorrow" on the shelf instead of the flat "0 due now", which reads as a
	// deck with nothing left in it rather than one that is merely resting.
	NextDue string `json:"next_due,omitempty"`

	// Unlocked is whether the learner may open the deck. Requires* say what it is
	// waiting for when they may not: which deck gates it, under what title, and
	// the progress through that deck as a have/need pair so the client can show a
	// bar rather than a closed door. Requires is empty and the numbers are 0 when
	// nothing gates the deck.
	Unlocked      bool   `json:"unlocked"`
	Requires      string `json:"requires"`
	RequiresTitle string `json:"requires_title"`
	RequiresSeen  int    `json:"requires_seen"`
	RequiresNeed  int    `json:"requires_need"`

	// IntroSeen is how the client decides whether to show the introduction
	// unprompted. It is derived from whether the learner has any progress in the
	// deck rather than stored: opening a deck and studying it are the same act,
	// and a learner who backed out of the intro without answering anything is
	// someone who should be shown it again.
	IntroSeen bool `json:"intro_seen"`

	Intro introResponse `json:"intro"`
}

// introResponse is the authored explanation shown before a deck's first session.
type introResponse struct {
	Heading string               `json:"heading"`
	Body    []string             `json:"body"`
	Groups  []introGroupResponse `json:"groups"`
	Closing string               `json:"closing"`
}

// introGroupResponse is one block of the explanation. Answer names one of the
// deck's Answers, or is empty, so the client can colour a block to match the
// button it leads to.
type introGroupResponse struct {
	Answer  string `json:"answer"`
	Label   string `json:"label"`
	Members string `json:"members"`
	Hook    string `json:"hook"`
}
