package studyapp

import (
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

// fromBusNounResponse flattens a Business Noun into a wire card, converting the
// strong Article to its string form explicitly.
func fromBusNounResponse(n vocabbus.Noun) batchCardResponse {
	return batchCardResponse{
		Lemma:   n.Lemma,
		Article: n.Article.String(),
		Gloss:   n.Gloss,
	}
}
