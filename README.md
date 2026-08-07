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
-addr        address to listen on (default "127.0.0.1:8080"; keep it on loopback
             behind a TLS proxy, since the session cookie and the one-time token
             must never cross a plain-HTTP hop)
-link-base   absolute URL of the sign-in callback, e.g.
             https://uebung.club/auth/callback
-trust-proxy believe X-Forwarded-For for rate limiting. Only with a trusted proxy
             in front: unconditionally trusting it lets anyone forge a fresh
             client address per request and walk past the rate limiter. It has no
             say over cookies -- see below
-insecure-cookies
             DEVELOPMENT ONLY: issue session cookies without Secure, for
             plain-HTTP localhost
-smtp-host   SMTP host; empty logs the link instead of sending it
-single-user DEVELOPMENT ONLY: skip sign-in, everyone is the same learner
-data       progress database (default "uebung.db")
-batch      cards per preloaded batch (default 20)
-retention  FSRS desired retention, 0<r<1 (default 0.9)
```

## Accounts

There are no passwords. You give an address, we email a link, following it signs
you in. That makes the address the account, so it is normalised on the way in —
otherwise `John@x.com` and `john@x.com` are two decks.

```
POST /auth/request   {"email": "..."}   always 202, whatever the address
GET  /auth/callback?token=...           303 to / with a session cookie
POST /auth/logout                       destroys the session server-side
GET  /auth/me                           who is signed in, or 401
```

Some deliberate choices worth knowing before changing any of it:

- **A link is confirmed, not auto-redeemed.** `GET /auth/callback` shows "continue
  as `<address>`?"; only the `POST` signs you in. Following a link is something an
  attacker can make your browser do, and signup is open — so redeeming on GET
  would let someone steer you into *their* account and collect everything you
  studied. The confirmation is protected by a `SameSite=Strict` nonce cookie, so a
  cross-site auto-submitting form cannot stand in for you. Opening a link on a
  different device from the one that requested it still works.
- **`/auth/request` answers the same way for every well-formed address** — known,
  unknown, or rate-limited. Anything else turns it into a "does this person have
  an account here" oracle.
- **Tokens and session ids are stored only as SHA-256 hashes.** A database dump
  yields nothing replayable. Plain SHA-256 rather than argon2 is right *because*
  these are 256-bit random values: there is no dictionary to attack.
- **A link works once**, enforced by a conditional `UPDATE` in the store rather
  than a read-then-write, so a double-clicked link cannot mint two sessions.
- **Every failure in the callback looks identical** — expired, spent and forged
  all redirect to `/?signin=expired`.
- **Rate limits are per client address and per target address**, both charged.
  Signup is open, so this endpoint makes our mail server send mail on a
  stranger's say-so; the limits protect the domain's sending reputation.

Run it without a mail server: with no `-smtp-host`, the link is written to the
log. `-single-user` skips sign-in entirely and makes every visitor the built-in
local learner — **development only**, it makes every visitor the same person.

### Cookies and the reverse proxy

Session cookies carry `Secure` and the `__Host-` prefix whenever `-link-base` is
an `https://` URL. **This is decided once at startup, never per request.** It used
to be inferred from `X-Forwarded-Proto`, which was a trap: behind Apache `r.TLS`
is nil on every request and `mod_proxy_http` does not send that header, so the app
silently issued 90-day session cookies with neither protection — and nothing
looked broken, because the site still worked over HTTPS.

Running on plain-HTTP localhost, pass an `http://` link base (or
`-insecure-cookies`); the server warns on startup when cookies will lack `Secure`.

Two things still belong on the proxy, because the app cannot set them for you:

```apache
# So the app sees the real client address for rate limiting (with -trust-proxy).
RequestHeader set X-Forwarded-Proto "https"
# So a browser never tries plain HTTP in the first place.
Header always set Strict-Transport-Security "max-age=31536000; includeSubDomains"
```

Use `set`, not `setifempty`: with `setifempty` a client can supply its own value.

### Bringing a pre-accounts deck across

Progress recorded before accounts existed is keyed to the `local` learner and
would otherwise be stranded. Sign in once, stop the server, then:

```bash
uebung-claim -data uebung.db -email you@example.com -dry-run   # look first
uebung-claim -data uebung.db -email you@example.com
```

It refuses unless the account exists and has followed a link, which is what
proves the address is yours. A card the account has already studied keeps its own
newer scheduling rather than being overwritten.

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
