package studyapp

// This file holds the App-layer wire types and their converters. Every field
// here is a primitive — string, int, bool — because this is the API edge, where
// JSON is spoken and no business/types strong type is allowed to appear. Parsing
// and validation of incoming primitives happens in the toBus* converters and
// nowhere else; outgoing strong types are flattened explicitly in the fromBus*
// builders, never by leaning on a strong type's own JSON marshaling.

// batchCardResponse is one question as the client preloads it. The correct
// article travels with the card on purpose: this is self-paced practice, not a
// graded exam, so the browser can reveal the answer and grade the swipe with
// zero round-trips. Gloss is shown as feedback once the learner has answered.
//
// Example and ExampleEn ride along for the same reason. They are only needed for
// the nouns a learner gets wrong, which is not known until the round is over —
// but fetching them then would put a network round-trip between the last swipe
// and the result panel. Shipping them with the batch keeps the whole round local.
type batchCardResponse struct {
	Lemma     string `json:"lemma"`
	Article   string `json:"article"`
	Gloss     string `json:"gloss"`
	Example   string `json:"example"`
	ExampleEn string `json:"example_en"`
}

// batchResponse is a whole preloaded study session: the ordered questions plus
// the language they belong to, so the client can echo it back on the flush.
type batchResponse struct {
	Lang  string              `json:"lang"`
	Cards []batchCardResponse `json:"cards"`
}

// gradeRequest is the batched flush the client sends after working through the
// preloaded cards. Results are graded client-side (correct/incorrect, adjusted
// by answer speed) into an FSRS rating name, and the whole set posts at once so
// each swipe stayed local and instant.
type gradeRequest struct {
	Lang    string         `json:"lang"`
	Results []gradeOutcome `json:"results"`
}

// gradeOutcome is one graded card in a flush: which lemma, and how it went.
type gradeOutcome struct {
	Lemma  string `json:"lemma"`
	Rating string `json:"rating"` // one of: again, hard, good, easy.
}

// gradeResponse reports the state of the deck after a flush was applied, so the
// client can show progress and decide whether to request another batch.
type gradeResponse struct {
	Applied  int `json:"applied"`   // cards successfully graded.
	DueNow   int `json:"due_now"`   // reviews already due again.
	Learned  int `json:"learned"`   // cards ever reviewed at least once.
	DeckSize int `json:"deck_size"` // total nouns in the language's deck.
}

// summaryResponse is a lightweight progress snapshot for the language.
type summaryResponse struct {
	Lang     string `json:"lang"`
	DeckSize int    `json:"deck_size"`
	Learned  int    `json:"learned"`
	DueNow   int    `json:"due_now"`
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
