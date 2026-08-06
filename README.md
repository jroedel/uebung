# Übung

A spaced-repetition trainer for German grammatical gender — **der / die / das** —
drilled on the nouns that turn up most often in film and television subtitles.

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

Progress is saved to `uebung-data.json` in the working directory. Flags:

```
-addr       address to listen on (default ":8080")
-data       progress store file (default "uebung-data.json")
-batch      cards per preloaded batch (default 20)
-retention  FSRS desired retention, 0<r<1 (default 0.9)
```

## How a session works

1. The browser asks `GET /api/batch?lang=de` once and receives an ordered set of
   cards — due reviews first, then a capped number of new nouns — each carrying
   its correct article and gloss.
2. You answer every card locally. A miss is graded *again*; a hit is graded by
   how fast it came (*easy* / *good* / *hard*). Nothing hits the network.
3. When the batch is done the client flushes every grade in one
   `POST /api/grade`. The server runs each through FSRS and persists the result.

`GET /api/summary?lang=de` reports deck size, nouns seen, and reviews due now.

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
  stores/filedb, stores/memdb  JSON-file persistence; in-memory for tests
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

### Storage: today and next

Persistence sits behind `studybus.Storer`. The shipping default is `filedb`, a
stdlib-only JSON store — no driver, no server, fully testable offline. A
SQLite-backed store is the intended next step and is a drop-in: it implements the
same `Storer`, so swapping it in is a one-line change in `cmd/uebung/main.go`
with no change to the business or app layers.

> This first cut targets **Go 1.24** and uses the file store because the
> environment it was built in could reach neither the Go 1.26 toolchain nor the
> SQLite driver. Both are single, isolated bumps: raise the `go` line in `go.mod`
> and add a `stores/sqlitedb` package.

## Development

```bash
make test     # unit tests + vet + gofmt check
make lint     # vet + gofmt check
make build    # ./uebung
```

The deck genders are hand-verified; corrections to
`business/domain/vocab/stores/seeddb/nouns_de.json` are welcome, and the seed
test guards against a malformed edit shipping.
