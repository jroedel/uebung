package studyapp

import (
	"slices"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
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

// gradedResult is one validated flush entry: a lemma and its parsed rating.
type gradedResult struct {
	lemma  string
	rating rating.Rating
}

// toBusGradeRequest validates a whole flush. It reports the language and every
// malformed result in one pass, so a client that sent three bad ratings learns
// all three at once rather than one retry at a time. An empty lemma or an
// unknown rating name fails its row; a row index is included so the client can
// point at the offender.
func toBusGradeRequest(req gradeRequest) (langcode.LangCode, []gradedResult, error) {
	var fes errs.FieldErrors

	lang, err := langcode.Parse(req.Lang)
	fes.Add("lang", err)

	results := make([]gradedResult, 0, len(req.Results))
	for i, r := range req.Results {
		if r.Lemma == "" {
			fes.Addf("results", "row %d: empty lemma", i)
			continue
		}

		rat, err := rating.Parse(r.Rating)
		if err != nil {
			fes.Addf("results", "row %d (%s): %v", i, r.Lemma, err)
			continue
		}

		results = append(results, gradedResult{lemma: r.Lemma, rating: rat})
	}

	return lang, results, fes.ErrorOrNil()
}

// fromBusDeckResponse flattens a catalog Deck, its gate and the learner's
// standing in it into one wire entry. Every strong type is stringified here and
// nowhere else; the counts arrive already computed because they are composed from
// two domains and that composition is the handler's job, not a converter's.
func fromBusDeckResponse(d curriculumbus.Deck, g curriculumbus.Gate, requiresTitle string, size, learned, dueNow int) deckResponse {
	return deckResponse{
		ID:       d.ID.String(),
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Drill:    d.Drill.String(),
		Answers:  slices.Clone(d.Answers),

		DeckSize: size,
		Learned:  learned,
		DueNow:   dueNow,

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
func fromBusNounResponse(n vocabbus.Noun) batchCardResponse {
	return batchCardResponse{
		Lemma:     n.Lemma,
		Article:   n.Article.String(),
		Gloss:     n.Gloss,
		Example:   n.Example,
		ExampleEn: n.ExampleEn,
	}
}
