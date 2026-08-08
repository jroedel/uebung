package studyapp

import (
	"fmt"
	"slices"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/domain/trigger/triggerbus"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
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

// gradedResult is one validated flush entry: a card's key and its parsed rating.
type gradedResult struct {
	item   string
	rating rating.Rating
}

// toBusGradeRequest validates a whole flush. It reports the language, the deck and
// every malformed result in one pass, so a client that sent three bad ratings
// learns all three at once rather than one retry at a time. A missing key or an
// unknown rating name fails its row; a row index is included so the client can
// point at the offender.
func toBusGradeRequest(req gradeRequest) (langcode.LangCode, deckid.DeckID, []gradedResult, error) {
	var fes errs.FieldErrors

	lang, err := langcode.Parse(req.Lang)
	fes.Add("lang", err)

	deck, err := toBusDeck(req.Deck)
	fes.Add("deck", err)

	results := make([]gradedResult, 0, len(req.Results))
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

		results = append(results, gradedResult{item: item, rating: rat})
	}

	return lang, deck, results, fes.ErrorOrNil()
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
