package vocabbus

import (
	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/langcode"
)

// Noun is one deck entry: a German noun, the gender a learner must produce for
// it, a short English gloss shown once the answer is revealed, and an example
// sentence used to review the nouns a learner got wrong.
//
// Lemma is the noun's dictionary form and doubles as its identity within a
// language. The deck is curated data with no repeats, so the lemma is a natural
// key — a study record points back to a card by (language, lemma) rather than by
// a separate synthetic id. Article is the answer; Gloss is context, never graded.
//
// Example puts the noun in a natural sentence, which means the article there may
// be declined ("Ich kenne den Mann nicht.") and so is not necessarily the article
// being drilled. The nominative form for reference is composed from Article and
// Lemma at the point of display rather than stored, so it cannot drift away from
// the gender this deck actually teaches.
type Noun struct {
	Lemma     string            // dictionary form, e.g. "Haus"; unique within Lang.
	Article   article.Article   // the gender the learner must supply.
	Gloss     string            // short English meaning, for feedback only.
	Example   string            // sentence using Lemma, for post-round review.
	ExampleEn string            // English translation of Example.
	Lang      langcode.LangCode // which language's deck this belongs to.
}
