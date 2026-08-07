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
