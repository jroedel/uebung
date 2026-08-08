// Package seeddata holds the authored course content and nothing else.
//
// It exists because one authored file can serve more than one domain. triggers_de.json
// describes two decks *and* carries the items they are drilled from: the catalog
// entry is the curriculum domain's business, the items are the trigger domain's,
// and they are written together because a deck and its material are edited
// together. Neither domain's store can embed a file living in the other's
// directory — go:embed cannot reach outside its own package — so the content
// moves to a package below both of them instead of being copied into each.
//
// This is a leaf: it embeds bytes and imports nothing from business/domain or
// app, so any layer may read it and no layer is coupled to another through it. It
// deliberately does no parsing. What a row means, and which strong types it must
// validate into, is a decision for whichever Storage layer is reading it — the
// same trigger row becomes a deck's Size in one store and a Trigger in another.
package seeddata

import "embed"

// Files holds every authored deck file, embedded in the binary so the program
// ships its own course with no external file to deploy and no database to seed.
//
//go:embed articles_de.json nouns_de.json triggers_de.json
var Files embed.FS
