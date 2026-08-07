package nickname

import "strings"

// Screening for names that should not appear on a public leaderboard.
//
// Two lists, for two different problems:
//
//   - reserved names are ones that would let someone pass themselves off as the
//     site or its staff. "Admin" at rank 4 is not offensive, it is misleading,
//     and the person who picks it is usually picking it for that reason.
//   - blocked names are slurs and profanity. This list will never be complete —
//     that is not a reason to skip it, but it is a reason not to pretend
//     otherwise. It catches the unimaginative, which is most of it.
//
// What this deliberately does not do is chase evasion. Leetspeak folding,
// substring matching and edit-distance checks all buy a little more coverage at
// the price of rejecting real names, and a learner told "please choose a
// different name" about their own perfectly ordinary one has no way to find out
// why. Whole-word matching keeps the false-positive rate at approximately zero,
// which matters more here than the last few percent of recall. If names people
// actually pick turn out to need more, the answer is a report button and a
// human, not a cleverer regexp.
//
// Matching runs on the folded form (see Nickname.Fold), so case, spacing and
// hyphens do not help: "A-D-M-I-N" folds to "admin" like everything else.

// reserved names, matched exactly against the whole folded name or any single
// word within it — so both "Admin" and "Admin Helper" are refused.
var reserved = words(
	// The site itself, in both spellings, since "ue" is the standard ASCII
	// transliteration of "ü" and folding keeps them distinct.
	"uebung", "übung", "uebungclub", "übungclub", "club",

	// Roles.
	"admin", "administrator", "administratorin", "moderator", "moderatorin",
	"mod", "staff", "team", "support", "helpdesk", "official", "offiziell",
	"owner", "root", "superuser", "sysadmin", "operator",

	// Things that read as the software talking rather than a person.
	"system", "server", "bot", "robot", "service", "notification",
	"security", "sicherheit", "billing", "payment",

	// Route and protocol words, so a name never reads like a link or a state.
	"api", "auth", "login", "logout", "signin", "signup", "register",
	"password", "passwort", "account", "konto", "settings", "help", "hilfe",

	// Absence, which should never render as somebody's name.
	"null", "nil", "none", "undefined", "unknown", "anonymous", "anonym",
	"deleted", "geloescht", "gelöscht", "guest", "gast", "me", "you",
)

// blocked names: slurs and profanity, German and English, matched the same way.
//
// Entries are stored in folded form (lowercase, "ß" already expanded to "ss"),
// because that is what they are compared against. Inflections are listed
// explicitly where they are common rather than being derived, since German
// endings would need a stemmer and a stemmer would take innocent words with it.
var blocked = words(
	// English profanity.
	"fuck", "fucker", "fucking", "motherfucker", "shit", "bullshit", "shithead",
	"cunt", "twat", "wanker", "bastard", "bitch", "whore", "slut", "dick",
	"dickhead", "cock", "prick", "asshole", "arsehole", "arse", "piss",
	"pissing", "damn", "crap", "douche", "douchebag", "jackass",
	// "ass" is deliberately absent: "das Ass" is ordinary German for "ace", and
	// it makes a good name. The compounds above carry the offensive sense.

	// German profanity.
	"scheisse", "scheiss", "kacke", "arsch", "arschloch", "arschlecker",
	"wichser", "wichsen", "fotze", "fick", "ficker", "ficken", "hure",
	"hurensohn", "nutte", "schlampe", "mistkerl", "drecksau", "schwanz",
	"pimmel", "titten", "moese", "möse", "pisser", "pisse", "kotze",

	// Slurs. Kept short and unambiguous on purpose: every entry here is a word
	// with no innocent reading, so no real name is lost to it.
	"nigger", "nigga", "neger", "faggot", "fag", "tranny", "retard", "retarded",
	"spastic", "spasti", "kanake", "zigeuner", "schwuchtel", "chink",
	"paki", "wetback", "gook", "mongoloid", "mongo", "spast",
	// "kraut" is deliberately absent: it is an anti-German slur only in English,
	// while "das Kraut" is everyday German for a herb. In a German deck the
	// innocent reading is the likely one.

	// Extremist references, which a leaderboard is a favourite place to plant.
	"hitler", "nazi", "nazis", "heilhitler", "siegheil", "hakenkreuz",
	"holocaust", "genocide", "voelkermord", "völkermord", "isis", "kkk",
	"1488",
	// "88" alone is deliberately absent. It is a known code, but it is also the
	// year a large share of the people using this app were born, and "Eule 88"
	// being refused with no explanation is the exact false positive this file
	// argues against. "1488" has no such innocent reading.
)

// screen rejects a normalised name that matches either list.
//
// A name is checked twice: as a whole, and word by word. The whole-name check
// catches a word split up to sneak past — "A d m i n" folds to "admin" with no
// words left to match — and the per-word check catches an entry buried in an
// otherwise ordinary name, like "Blaue Nazi Eule".
func screen(name string) error {
	whole := Nickname{value: name}.Fold()
	parts := foldedWords(name)

	if reserved[whole] || containsAny(parts, reserved) {
		return ErrReservedNickname
	}

	if blocked[whole] || containsAny(parts, blocked) {
		return ErrProfaneNickname
	}

	return nil
}

// foldedWords splits name on its separators and folds each part the same way
// Nickname.Fold folds the whole, so the two comparisons agree on spelling.
func foldedWords(name string) []string {
	lowered := strings.ToLower(name)
	lowered = strings.ReplaceAll(lowered, "ß", "ss")

	return strings.FieldsFunc(lowered, isSeparator)
}

func containsAny(parts []string, set map[string]bool) bool {
	for _, p := range parts {
		if set[p] {
			return true
		}
	}

	return false
}

// words builds a lookup set from a literal list, so the lists above stay
// readable as lists.
func words(list ...string) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, w := range list {
		set[w] = true
	}

	return set
}
