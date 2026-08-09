# Übung Club

A spaced-repetition trainer for German grammatical gender — **der / die / das** —
drilled on the nouns that turn up most often in film and television subtitles.
Runs at [uebung.club](https://uebung.club).

The first deck is German gender: the ~200 highest-frequency subtitle nouns, each
scheduled with **FSRS** (the Free Spaced Repetition Scheduler) so you review each
noun exactly as often as your memory of it needs. The browser client is a swipe
game — flick a card left for *der*, up for *das*, right for *die* — and because a
whole batch is preloaded with its answers, every swipe is graded instantly with no
network round-trip.

Behind it sits a **course**: an ordered shelf of decks, each with an introduction
explaining the pattern behind its material, and each unlocked by working through
the one before it. Two more follow the nouns — which case a preposition takes, and
which case a verb takes — drilled with the same three-way swipe over *Akkusativ*,
*Genitiv* and *Dativ*.

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
POST /auth/nickname  {"nickname":"..."} sets or changes the display name
POST /auth/nickname/skip                accepts a generated one instead
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

### Nicknames

The address is private; the nickname is the public name, and it exists because a
leaderboard has to print something. Nothing derives one from the other — an
account signing in as `firstname.lastname@work.example` should not find their
full name at the top of a public list.

A learner is asked once, after signing in, and can skip: skipping takes a
generated German name — `Blaue Eule`, `Dunkler Hund` — from an adjective and a
noun, with the adjective declined to agree with the noun's gender. The word
lists live in `business/domain/identity/nicknamer` and are validated as they
load; the package test walks all ~112,000 pairs and checks each is a name a
person could have typed themselves.

- **Uniqueness is decided on a folded form** — lowercased, `ß` expanded to `ss`,
  spaces and hyphens dropped — so `Blaue Eule`, `blaue-eule` and `BlaueEule` are
  one name. Two names a reader cannot tell apart in a column are a collision,
  whatever the bytes say. Umlauts are deliberately *not* folded to base vowels:
  `Grüne` and `Grune` are different German words.
- **The character set is an allowlist**, not a filter: ASCII letters, German
  umlauts and `ß`, digits, single spaces and hyphens. A leaderboard is exactly
  where a homoglyph pays off — Cyrillic `а` renders identically to Latin `a` —
  and an allowlist rules that out by construction rather than by a confusables
  table someone has to keep current.
- **Skipping claims the name on the button.** Someone who pressed "Be Blaue
  Eule" agreed to a specific name, so that is the name they get. The suggestion
  itself reserves nothing — holding a reservation would need its own expiry and
  cleanup — so if it was taken in the meantime the server quietly picks another.
  The client always reads the assigned name back from the response rather than
  assuming it.
- **Screening is whole-word**, against a reserved list (impersonation: `admin`,
  `uebung`, `support`) and a profanity list. It will never be complete, and it
  is tuned to avoid false positives rather than to maximise recall — `das Ass`
  and `das Kraut` are ordinary German, and `Eule 88` is usually a birth year.
- **Renaming frees the previous name immediately**, and is not rate-limited.
  Both are worth revisiting once names are publicly visible and therefore
  publicly worth squatting.

The column is added to an existing `users` table by a guarded `ALTER TABLE` in
the store's `Open`. Its unique index is **partial** — `WHERE nickname_fold <> ''`
— because every pre-existing row has an empty fold and a plain unique index
would find them all in conflict with one another.

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

The review log moves in full, including the reviews of any card left behind by
that rule. Those are still things the same person did — the claim is a statement
that the anonymous deck was always theirs — and the log is a history rather than a
key, so nothing collides.

## Decks, introductions and unlocking

`GET /api/decks?lang=de` is the shelf: every deck in the course, in order, each
with its title, its drill, the learner's progress in it, whether it is open, and
its whole introduction. One request builds the picker.

An **introduction** is the deck's orienting explanation — the pattern behind the
material rather than a rule to memorise — shown before the first session and
readable again at any time from the header. It is authored, structured data
(heading, body, colour-coded groups, a closing line for when you are stuck), not
prose in a template, so a deck cannot ship without one and a group cannot promise
to explain an answer the deck never asks for: both fail at start-up.

A deck **unlocks** when four fifths of the deck before it has been seen. The
measure is deliberately coverage — cards answered at least once — and not
retention, because coverage only ever goes up. A gate built on how well you
currently remember things would re-lock a deck the day you lapsed a few cards in
the one before it, which is the worst thing an unlock rule can do. The honest
reading is "you have worked through enough of the previous deck to be ready for
this one", and the fraction is `-unlock-fraction` rather than a constant anyone
has to believe in.

Nothing about unlocking is stored. It is derived from study records on every
request, so there is no unlock state that can drift out of step with the course
when a deck is added, reordered, or regated.

The client decides for itself which drills it can render, from the `drill` field.
A deck whose drill this build does not know still appears with its introduction —
that writing is worth reading before the drill exists — but cannot be started.
Whether a browser can draw a deck is a fact about the browser, so the server never
sends a "playable" flag it would be guessing at. Both drills in the course today,
`article-3way` and `case-3way`, are renderable; the mechanism is what lets a third
ship its introduction ahead of its drill.

## How a session works

1. The browser asks `GET /api/batch?lang=de&deck=...` once and receives an ordered
   set of cards — due reviews first, then a capped number of new ones — each
   carrying its correct answer, gloss, and example sentence.
2. You answer every card locally. A miss is graded *again*; a hit is graded by
   how fast it came (*easy* / *good* / *hard*). Nothing hits the network.
   A hit clears in 0.9s. A miss holds still, shows the sentence, and then drifts
   away slowly in the direction that would have been right — about 1.6s in all,
   because a miss is the one moment in a round where there is anything to learn.
   Any key or tap skips the rest of a reveal, so knowing the answer still lets
   you move at speed.
3. When the batch is done the client flushes every grade in one
   `POST /api/grade`. The server runs each through FSRS and persists the result.
4. The round ends on the cards you got wrong, each shown in a sentence with its
   translation, so a miss is corrected in context rather than just counted.

The example sentences use their noun in a natural case, which means the article
*in the sentence* may be declined — "Ich kenne den Mann nicht." The nominative
is appended for reference, "(der Mann)", and is composed at display time from
the card's own article and lemma, so it can never disagree with the gender the
deck teaches.

A case deck answers the same way with different material. Its cards are
**triggers** — a preposition or a verb — and the answer worth remembering is not
the label but the declined phrase, so a miss reads back "durch den Park —
Akkusativ" rather than the case name alone. The three answers sit in the same
three directions, chosen the same way: the two horizontal swipes take the
commonest answers and the vertical flick the rarest, which is *das* for gender and
the genitive for case.

They do **not** wear the same colours, and that is the one thing about the palette
worth knowing. A colour is bound to a grammatical concept for the life of the app,
never to the slot an answer happens to sit in. Gender keeps the German classroom
convention — *der* blue, *die* rose, *das* green — and case has its own family,
sited in the hue gaps that trio leaves: Akkusativ amber, Dativ cyan, Genitiv
violet, with Nominativ reserved as an unsaturated steel for the first deck that
asks for it.

The arrangement this replaced treated the three colours as positional slots, so a
case deck reused the gender palette on the argument that neither meant anything
about the other. That holds for a stylesheet and fails for a learner: the noun
deck spends 213 cards teaching *blue = der*, and colour-coded gender is a
classroom convention precisely because it sticks. Overwriting it is worse than
never using colour at all. Keeping the families apart also leaves room for a deck
that asks for a case *and* a gender on one card — "akkusativ-feminin" — since the
two are drawn from different palettes rather than competing for the same three.

The mapping lives entirely in `styles.css`, keyed off a `data-answer` attribute.
Adding a deck is adding a rule there; the client script never learns that the
dative is cyan.

`/api/batch`, `/api/grade` and `/api/summary` all take a `deck`. Omitting it means
the noun deck — every request made before the course had a second drill left it
out, and every one of them meant that deck, so defaulting is what keeps a cached
client working. `/api/grade` accepts a card's key as either `item` or, for the
same reason, the older `lemma`; sending both is an error rather than a silent
preference.

A locked deck is refused with 403 by `/api/batch` and `/api/grade`, not merely
greyed out on the shelf. A gate enforced only in the browser is a suggestion, and
grading is the half that matters: progress in a locked deck is exactly what
unlocks the deck after it.

`GET /api/summary?lang=de&deck=...` reports deck size, cards seen, reviews due
now, and — when nothing is due — when the next review lands, so a finished deck
can say "next review in 16 days" instead of "0 due" and a dead *Next batch*
button.

## Where you slip

Every graded answer records **which answer the learner actually gave** and **how
long it took**, alongside the rating. `GET /api/confusion?lang=de&deck=...` turns
that into a confusion matrix — a dense grid in the deck's own answer order, so
`cells[i][j]` is the number of times the deck wanted answer *i* and the learner
said answer *j*, and the diagonal is the times they were right.

The rating cannot answer this. Every miss is `again` whatever was swiped, so a log
of ratings can count mistakes and never characterise them. The interesting fact
for someone who has already had German classes is not *how many* they get wrong —
they know they are shaky — but **which way**: that they turn feminines masculine
two and a half times as often as the reverse, and so which half of the pair is
worth the work. That is the one thing practice cannot show you about yourself,
because practice is what produces it. The same argument is already written out in
`business/types/roleanswer` for the two-part cards; this applies it to every deck
that offers a choice.

Answer times are kept for the reason the audience is who it is. Someone
refreshing German does not become *more* correct on "der Mann" — they were already
correct. They become faster, and the crossing from deliberate recall to automatic
retrieval is what the practice is actually for. The client already measured it to
choose between *easy*, *good* and *hard* and then discarded the number.

Some deliberate choices:

- **The expected answer is not stored.** It is a fact about the deck, not about
  the learner, and it is already known wherever the deck's material is loaded.
  Copying it into the log would freeze a mistake in the authored data into every
  row written before it was fixed. The App layer pairs each counted item back with
  its answer, exactly as a batch does — which is why the study domain still knows
  nothing about nouns.
- **The answer is validated against the deck's own `answers` before it is
  written.** The log only ever grows, so a reply the deck does not offer cannot be
  tidied up afterwards; it has to be refused at the door.
- **Both fields are optional, and empty means "not reported".** A browser holding
  a cached client from before this release sends neither, and refusing those
  flushes would cost that learner the round they had just finished. Zero
  milliseconds is not a reachable answer time, so it is unambiguous.
- **Neither field touches scheduling.** FSRS sees the rating and nothing else, so
  two identical grades advance a card identically however fast they were answered.
- **The grouping is SQL's**, not Go's. The log grows with every swipe a learner
  ever makes; the matrix is bounded by deck size times answer count.

The columns are added to an existing `study_review` by a guarded `ALTER TABLE` in
the store's `Open` — neither belongs to a key, so this is a plain in-place add
rather than the rebuild `study_progress` needed. Rows written before the migration
take the defaults, which read back as "not reported": true, rather than an answer
nobody gave.
`GET /healthz` reports whether the app can serve, not whether it is listening. It
selects the app's real column lists from both study tables, deliberately rather
than pinging: a ping proves a connection is alive and reads nothing, so it answers
200 against a schema the binary cannot use — which is the state a rolled-back
deploy leaves behind, and the one case where a wrong health check costs the most.

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
app/studyapp                   HTTP handlers; the only layer that knows every domain
  static/                      embedded client (index.html, app.js, styles.css)
business/domain/curriculum     the course (which decks exist, in what order, gated how)
  curriculumbus, stores/seeddb embedded catalog + introductions; the unlock rule
business/domain/vocab          the noun deck (what the genders are)
  vocabbus, stores/seeddb      embedded curated JSON deck
business/domain/study          scheduling (when to show each card)
  studybus                     FSRS-driven selection + grading, over opaque deck items
  stores/sqlitedb, memdb       SQLite persistence; in-memory for tests
business/types                 strong types: article, rating, cardstate, userid,
                               langcode, deckid, drillkind, roleanswer
foundation/fsrs                self-contained FSRS-5 scheduler
foundation/errs                field-error accumulation for converters
```

Three deliberate boundaries make future modules cheap:

- **study never imports vocab.** The scheduler works on opaque item strings scoped
  by a `deckid.DeckID`; the App layer pairs a scheduled item back with its noun. A
  new deck (prepositions, a second language) is new data behind the same
  scheduler, and the deck in the key is what stops two decks that happen to share
  an item key — "mit" as a preposition, "mit" as anything else — from colliding.
- **the scheduler is foundation.** `foundation/fsrs` is pure arithmetic and knows
  nothing about German; the study domain converts to and from it at one seam.
- **curriculum knows nothing about a learner.** It states the course and the
  unlock rule and holds no progress: a learner's standing arrives as a value the
  App assembles from the study domain. That is what makes the rule testable
  without a database and changeable without a migration.

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
`(user, lang, deck, item)` key is enforced by the database.

There are two tables, and the difference between them is the point:

- **`study_progress`** is where each card stands *now* — one row per card,
  overwritten by every review.
- **`study_review`** is what *happened* — one row per graded answer, appended and
  never updated. Points, streaks, daily counts and a leaderboard are all questions
  about *when* someone studied, and `study_progress` cannot answer any of them: it
  holds one timestamp, the most recent. Keeping the events rather than a running
  score also means the scoring formula can change later without orphaning the
  history, which a counter column could never offer.

A grade writes to both in one transaction, which is why `Storer.Save` takes the
card and its review together: a crash must not be able to leave a card advanced
with no record of the answer that advanced it.

A database written before decks existed is migrated on `Open`. SQLite cannot add a
column to a primary key in place, so this is the documented rebuild — create,
copy, drop, rename — in a single transaction, and every existing row lands in
`der-die-das`, the deck it was always implicitly in. The check is the absence of a
`deck` column, so re-running it is a no-op and a fresh database never touches it.

One schema note: the two timestamps are stored as `RFC3339Nano` **text** in UTC,
not as integers, so a row is readable in a `sqlite3` shell and an unreviewed
card's zero time reads as `0001-01-01T00:00:00Z`. Because writes normalise to
UTC, a time comes back as the same instant in a different location —
`time.Time.Equal` is unaffected, but don't compare a round-tripped `Progress`
with `==`.

`memdb` remains for tests: it holds Business models directly, needs no file, and
keeps the whole suite runnable offline.

## Deploying

`deploy/deploy.sh` does the whole thing. **The script is committed; the server's
identity is not** — host, account and port come from `deploy/deploy.env`, which is
gitignored.

> **Humans only.** Every subcommand opens an SSH session to production, the
> read-only ones included. Agents must not run it; see *Production access* in
> [AGENTS.md](AGENTS.md), and [production_recon.md](production_recon.md) for what
> is already known about the server without needing to look.

[DEPLOYING.md](DEPLOYING.md) is the reusable version of all this — the ordered
setup, what a Go app has to do to live behind konsoleH's Apache, the file modes, and
a symptom-to-cause table. Start there for a *new* project on the same host.

```bash
cp deploy/deploy.env.example deploy/deploy.env   # then fill it in
deploy/deploy.sh probe      # what does this server support? read-only
deploy/deploy.sh install    # one-time: dirs, supervisor, cron, .htaccess
deploy/deploy.sh            # build, upload, restart, health-check
```

Also: `backup`, `status`, `logs`, and `--skip-tests`.

### How it fits together

Apache terminates TLS with the konsoleH Let's Encrypt certificate and proxies to
the app on **loopback only**, so the app never speaks TLS and is never reachable
except through the proxy. `deploy/uebung.htaccess` does that with a `RewriteRule
… [P]` — `ProxyPass` is illegal in `.htaccess`, the `[P]` flag is the supported
way on konsoleH.

That rule **excludes `/.well-known/`**, which matters more than it looks:
konsoleH's FileAuth writes certificate challenges into the document root, and a
catch-all proxy hands them to the app instead, which quietly breaks renewal.

**The document root is a `public/` subdirectory of the application directory:**

```
~/public_html/uebung.club/          binary, database, env, backups
~/public_html/uebung.club/public/   .htaccess and nothing else  <- document root
```

The nesting is the safety property: the served directory is *below* the one
holding `uebung.db`, which contains every account's address and their session
hashes. This needs the konsoleH document root set to `/uebung.club/public`
(Services → Server Configuration → Change document root).

Until that setting is changed Apache serves the application directory itself, and
the database is downloadable — so `deploy.sh` **refuses to run** unless it can
prove otherwise. It asks for `uebung.db`, `uebung.env` and `run.sh` over HTTPS and
requires a non-200 answer, because the document root is a panel setting and cannot
be checked from the server side.

### Secrets

The SMTP password is never uploaded and never appears in a flag — a command line
is visible in `ps` to every other account on the machine. It lives on the server
in `~/uebung/uebung.env` (mode 600), written once by hand; `deploy.sh` preserves
it across deploys and warns if it is missing. Without it the app logs sign-in
links instead of sending them, which is a survivable degraded state rather than a
refusal to start.

### Staying up without root

No root is needed. `SUPERVISOR=systemd` uses `systemctl --user` (run
`loginctl enable-linger <account>` once, or the service dies at logout);
`SUPERVISOR=nohup` uses `deploy/supervise.sh` with an `@reboot` entry plus a
five-minute watchdog. `deploy.sh probe` tells you which the host supports.

### Why the deploy is safe to repeat

The app is stopped, *then* the database is copied — a copy taken while SQLite is
mid-transaction can be torn, so this ordering is deliberate. The binary is
replaced by rename rather than rewritten in place (overwriting a running
executable gives `ETXTBSY`; swapping the inode does not), the previous one is
kept, and a failed `/healthz` check restores it, restarts, prints the tail of the
log and exits non-zero.

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
