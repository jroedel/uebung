package vocabbus

import (
	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/langcode"
)

// Noun is one deck entry: a German noun, the gender a learner must produce for
// it, and a short English gloss shown once the answer is revealed.
//
// Lemma is the noun's dictionary form and doubles as its identity within a
// language. The deck is curated data with no repeats, so the lemma is a natural
// key — a study record points back to a card by (language, lemma) rather than by
// a separate synthetic id. Article is the answer; Gloss is context, never graded.
type Noun struct {
	Lemma   string            // dictionary form, e.g. "Haus"; unique within Lang.
	Article article.Article   // the gender the learner must supply.
	Gloss   string            // short English meaning, for feedback only.
	Lang    langcode.LangCode // which language's deck this belongs to.
}
