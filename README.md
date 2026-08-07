# Übung Club

A spaced-repetition trainer for German grammatical gender — **der / die / das** —
drilled on the nouns that turn up most often in film and television subtitles.
Runs at [uebung.club](https://uebung.club).

Module 1 is the German gender deck: the ~200 highest-frequency subtitle nouns,
each scheduled with **FSRS** (the Free Spaced Repetition Scheduler) so you review
each noun exactly as often as your memory of it needs. The browser client is a
swipe game — flick a card left for *der*, up for *das*, right for *die* — and
because a whole batch is preloaded with its answers, every swipe is graded
instantly with no network round-trip. More languages and more modules are meant
to slot in behind the same machinery later.

## Quick start

```bash
make run              # serves on :8080
# then open http://localhost:8080
```

Progress is saved to a SQLite database, `uebung.db`, in the working directory.
Flags:

```
-addr       address to listen on (default ":8080")
-data       progress database (default "uebung.db")
-batch      cards per preloaded batch (default 20)
-retention  FSRS desired retention, 0<r<1 (default 0.9)
```

## How a session works

1. The browser asks `GET /api/batch?lang=de` once and receives an ordered set of
   cards — due reviews first, then a capped number of new nouns — each carrying
   its correct article, gloss, and example sentence.
2. You answer every card locally. A miss is graded *again*; a hit is graded by
   how fast it came (*easy* / *good* / *hard*). Nothing hits the network.
   A hit clears in 0.9s. A miss holds still, shows the sentence, and then drifts
   away slowly in the direction that would have been right — about 1.6s in all,
   because a miss is the one moment in a round where there is anything to learn.
   Any key or tap skips the rest of a reveal, so knowing the answer still lets
   you move at speed.
3. When the batch is done the client flushes every grade in one
   `POST /api/grade`. The server runs each through FSRS and persists the result.
4. The round ends on the nouns you got wrong, each shown in a sentence with its
   translation, so a miss is corrected in context rather than just counted.

The example sentences use their noun in a natural case, which means the article
*in the sentence* may be declined — "Ich kenne den Mann nicht." The nominative
is appended for reference, "(der Mann)", and is composed at display time from
the card's own article and lemma, so it can never disagree with the gender the
deck teaches.

`GET /api/summary?lang=de` reports deck size, nouns seen, and reviews due now.
`GET /healthz` reports whether the app can serve: with a store check wired it
touches the database, so a probe fails rather than returning 200 while every
study request errors.

### Installing it on a phone

The client is a PWA — manifest, icons and `theme-color` are embedded in the
binary like the rest of it — so a browser will offer to add it to the home
screen, where it opens without browser chrome. Installability needs HTTPS, so it
works from `uebung.club` but not from a plain-HTTP `localhost:8090` on a phone.

## Architecture

The project follows the same layered style as its sibling `adb-broker`: an App
layer that speaks HTTP, Business domains that hold the rules, and a Foundation of
reusable, dependency-free pieces. Primitives live at the edges (JSON, storage
rows); strong types live only in the Business layer; every boundary crossing goes
through a named converter.

```
cmd/uebung                     wires the layers, runs the HTTP server
app/studyapp                   HTTP handlers; the only layer that knows both domains
  static/                      embedded swipe client (index.html, app.js, styles.css)
business/domain/vocab          the noun deck (what the genders are)
  vocabbus, stores/seeddb      embedded curated JSON deck
business/domain/study          scheduling (when to show each card)
  studybus                     FSRS-driven selection + grading, over opaque lemmas
  stores/sqlitedb, memdb       SQLite persistence; in-memory for tests
business/types                 strong types: article, rating, cardstate, userid, langcode
foundation/fsrs                self-contained FSRS-5 scheduler
foundation/errs                field-error accumulation for converters
```

Two deliberate boundaries make future modules cheap:

- **study never imports vocab.** The scheduler works on opaque lemma strings; the
  App layer pairs a scheduled lemma back with its noun. A new deck (plurals, a
  second language) is new data behind the same scheduler.
- **the scheduler is foundation.** `foundation/fsrs` is pure arithmetic and knows
  nothing about German; the study domain converts to and from it at one seam.

### Multi-user readiness

The app runs single-user today. Every study record is nonetheless keyed by a
`userid.UserID`, and the one place identity is resolved
(`studyapp.App.currentUser`) returns the built-in `Local()` user. When accounts
arrive, that method starts returning a session-derived id and nothing downstream
changes — the storage schema and every business signature already speak in user
identity.

### Storage

Persistence sits behind `studybus.Storer`, and the shipping implementation is
`sqlitedb`. The driver is `modernc.org/sqlite` — pure Go, so there is no CGO and
no C toolchain in the build and `CGO_ENABLED=0` cross-compiles still work. A
review writes one row rather than rewriting a whole document, and the
`(user, lang, lemma)` key is enforced by the database.

One schema note: the two timestamps are stored as `RFC3339Nano` **text** in UTC,
not as integers, so a row is readable in a `sqlite3` shell and an unreviewed
card's zero time reads as `0001-01-01T00:00:00Z`. Because writes normalise to
UTC, a time comes back as the same instant in a different location —
`time.Time.Equal` is unaffected, but don't compare a round-tripped `Progress`
with `==`.

`memdb` remains for tests: it holds Business models directly, needs no file, and
keeps the whole suite runnable offline.

## Development

```bash
make test        # unit tests + lint + vulnerability scan
make test-unit   # unit tests alone
make lint        # vet + gofmt check
make vuln-check  # govulncheck against the Go vulnerability database
make build       # ./uebung
```

`govulncheck` is pinned as a tool dependency in `go.mod`, so `make vuln-check`
needs no separate install — but it does query the vulnerability database over
the network. Offline, run `make test-unit lint`.

The deck genders and example sentences are hand-written; corrections to
`business/domain/vocab/stores/seeddb/nouns_de.json` are welcome, and a native
review of the German is genuinely wanted. The seed test guards against a
malformed edit shipping: every noun must carry a gender, a gloss, a sentence and
a translation, and the sentence must actually contain its own noun — which is
the way a hand-edit most easily goes wrong.
