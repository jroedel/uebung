package studyapp

import (
	"fmt"
	"slices"
	"time"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/domain/trigger/triggerbus"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/foundation/errs"
)

// The App/Business converters. App → Business parses and validates primitives
// into strong types, accumulating every bad field at once; Business → App
// flattens strong types back to primitives explicitly.

// toBusLang parses a language query parameter into a strong LangCode.
func toBusLang(raw string) (langcode.LangCode, error) {
	var fes errs.FieldErrors

	lang, err := langcode.Parse(raw)
	fes.Add("lang", err)

	return lang, fes.ErrorOrNil()
}

// toBusDeck parses a deck identifier, treating the empty string as the noun deck.
//
// Defaulting rather than requiring the field is what keeps a cached client
// working: every request made before the course had more than one deck omits it,
// and every one of them meant the noun deck.
func toBusDeck(raw string) (deckid.DeckID, error) {
	if raw == "" {
		return defaultDeck, nil
	}

	return deckid.Parse(raw)
}

// toBusGradeRequest validates a whole flush. It reports the language, the deck and
// every malformed result in one pass, so a client that sent three bad ratings
// learns all three at once rather than one retry at a time. A missing key or an
// unknown rating name fails its row; a row index is included so the client can
// point at the offender.
//
// The answer a learner gave is deliberately *not* validated here. Whether "die" is
// a reply this deck offers is a question about the deck, and a converter that took
// the catalog as an argument would be doing the handler's composing for it — so
// this parses the shape and toBusGradeAnswers, which the handler calls once it has
// the deck in hand, checks the meaning.
func toBusGradeRequest(req gradeRequest) (langcode.LangCode, deckid.DeckID, []studybus.GradeInput, error) {
	var fes errs.FieldErrors

	lang, err := langcode.Parse(req.Lang)
	fes.Add("lang", err)

	deck, err := toBusDeck(req.Deck)
	fes.Add("deck", err)

	results := make([]studybus.GradeInput, 0, len(req.Results))
	for i, r := range req.Results {
		item, err := gradedItem(r)
		if err != nil {
			fes.Addf("results", "row %d: %v", i, err)
			continue
		}

		rat, err := rating.Parse(r.Rating)
		if err != nil {
			fes.Addf("results", "row %d (%s): %v", i, item, err)
			continue
		}

		// A negative time is a clock that went backwards or a subtraction the wrong
		// way round. Either way it is not an answer time, and admitting it would
		// poison every average taken over the column later.
		if r.AnswerMS < 0 {
			fes.Addf("results", "row %d (%s): answer_ms cannot be negative", i, item)
			continue
		}

		results = append(results, studybus.GradeInput{
			Item:   item,
			Rating: rat,

			// roleanswer.None: no deck in the course asks for a participant role
			// yet. The two-part cards that have a role half are still to be written.
			Role: roleanswer.None,

			Given:    r.Given,
			Answered: time.Duration(r.AnswerMS) * time.Millisecond,
		})
	}

	return lang, deck, results, fes.ErrorOrNil()
}

// toBusGradeAnswers checks each row's reported answer against the ones the deck
// actually offers, and is the guard that keeps the log worth reading.
//
// An answer that no deck accepts is not a harmless extra field: it becomes a
// column in that learner's error profile forever, and the profile is the whole
// reason the answer is recorded. The log is append-only, so a bad row cannot be
// tidied up afterwards — it has to be refused at the door.
//
// The empty string passes. It is what a browser holding a client from before this
// field existed sends, and rejecting the flush would cost that learner the round
// they had just finished to gain a fact about it that was never going to be there.
func toBusGradeAnswers(results []studybus.GradeInput, answers []string) error {
	var fes errs.FieldErrors

	for i, res := range results {
		if res.Given == "" || slices.Contains(answers, res.Given) {
			continue
		}

		fes.Addf("results", "row %d (%s): %q is not one of this deck's answers", i, res.Item, res.Given)
	}

	return fes.ErrorOrNil()
}

// fromBusConfusionResponse builds a learner's error profile for one deck.
//
// It is where the two halves meet. The study domain counted how often each item
// drew each answer and has no idea what any of them should have been; expected is
// the deck's own material, which this layer already loads to serve a batch. Pairing
// them here is the same composition handleBatch does, and it is why neither domain
// has to learn about the other.
//
// A count whose item is no longer in the deck, or whose answer the deck no longer
// offers, is dropped rather than forced into the grid. Both are the ordinary
// consequence of authored data being corrected after someone studied it, and a
// matrix is a statement about a deck as it stands.
func fromBusConfusionResponse(lang langcode.LangCode, d curriculumbus.Deck, counts []studybus.Confusion, expected map[string]string) confusionResponse {
	answers := slices.Clone(d.Answers)

	index := make(map[string]int, len(answers))
	for i, a := range answers {
		index[a] = i
	}

	cells := make([][]int, len(answers))
	for i := range cells {
		cells[i] = make([]int, len(answers))
	}

	recorded := 0
	for _, c := range counts {
		row, ok := index[expected[c.Item]]
		if !ok {
			continue
		}

		col, ok := index[c.Given]
		if !ok {
			continue
		}

		cells[row][col] += c.Count
		recorded += c.Count
	}

	return confusionResponse{
		Lang:     lang.String(),
		Deck:     d.ID.String(),
		Answers:  answers,
		Cells:    cells,
		Recorded: recorded,
	}
}

// gradedItem resolves a row's card key from the two field names the wire accepts.
//
// Both set is an error even when they agree. A client sending the same key twice
// is one mid-migration that has lost track of which field it owns, and the next
// thing it sends may well be two different keys — at which point silently
// preferring one would file a grade against a card the learner never saw.
func gradedItem(r gradeOutcome) (string, error) {
	switch {
	case r.Item != "" && r.Lemma != "":
		return "", fmt.Errorf("set item or lemma, not both")
	case r.Item != "":
		return r.Item, nil
	case r.Lemma != "":
		return r.Lemma, nil
	default:
		return "", fmt.Errorf("empty item")
	}
}

// fromBusDeckResponse flattens a catalog Deck, its gate and the learner's
// standing in it into one wire entry. Every strong type is stringified here and
// nowhere else; the counts arrive already computed because they are composed from
// two domains and that composition is the handler's job, not a converter's.
// nextDue arrives already formatted for the same reason — it is derived from the
// study domain's progress, which this converter never sees.
func fromBusDeckResponse(d curriculumbus.Deck, g curriculumbus.Gate, requiresTitle string, size, learned, dueNow int, nextDue string) deckResponse {
	return deckResponse{
		ID:       d.ID.String(),
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Drill:    d.Drill.String(),
		Answers:  slices.Clone(d.Answers),

		DeckSize: size,
		Learned:  learned,
		DueNow:   dueNow,
		NextDue:  nextDue,

		Unlocked:      g.Met,
		Requires:      g.Requires.String(),
		RequiresTitle: requiresTitle,
		RequiresSeen:  g.Seen,
		RequiresNeed:  g.Need,

		IntroSeen: learned > 0,

		Intro: fromBusIntroResponse(d.Intro),
	}
}

// fromBusIntroResponse flattens a deck's introduction.
func fromBusIntroResponse(in curriculumbus.Intro) introResponse {
	groups := make([]introGroupResponse, 0, len(in.Groups))
	for _, g := range in.Groups {
		groups = append(groups, introGroupResponse{
			Answer:  g.Answer,
			Label:   g.Label,
			Members: g.Members,
			Hook:    g.Hook,
		})
	}

	return introResponse{
		Heading: in.Heading,
		Body:    slices.Clone(in.Body),
		Groups:  groups,
		Closing: in.Closing,
	}
}

// fromBusNounResponse flattens a Business Noun into a wire card, converting the
// strong Article to its string form explicitly.
//
// It fills Lemma and Article as well as Item and Answer. See batchCardResponse for
// why the older pair is still written: a cached client reads them, and the noun
// deck is the only deck that ever had them.
func fromBusNounResponse(n vocabbus.Noun) batchCardResponse {
	return batchCardResponse{
		Item:      n.Lemma,
		Answer:    n.Article.String(),
		Gloss:     n.Gloss,
		Example:   n.Example,
		ExampleEn: n.ExampleEn,

		Lemma:   n.Lemma,
		Article: n.Article.String(),
	}
}

// fromBusTriggerResponse flattens a Business Trigger into a wire card, converting
// the strong Case to its string form explicitly.
//
// No Lemma or Article: a case card has neither, and a client old enough to want
// them cannot draw this deck regardless.
func fromBusTriggerResponse(t triggerbus.Trigger) batchCardResponse {
	return batchCardResponse{
		Item:      t.Word,
		Answer:    t.Case.String(),
		Gloss:     t.Gloss,
		Example:   t.Example,
		ExampleEn: t.ExampleEn,
		Phrase:    t.Phrase,
		Note:      t.Note,
	}
}
