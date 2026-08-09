# Design foundations

A front-end evaluation of Übung Club as it stands, and a proposal for the
elements worth building on before the shelf gets longer.

Written against commit `1a63355`, with the client exercised in a browser rather
than read: a fresh binary served on loopback, an account signed in through the
real link flow, both drills played to the end of a batch, at 430 px and at
1440 px. Everything asserted below was seen on screen.

## Status

The first tier has since been built. Where a section below describes something
that no longer holds, it is marked **— done**; the description is kept because
the reasoning is what makes the rest of the document readable, and because a
proposal that quietly edits away the problem it solved is hard to argue with
later.

Shipped:

- `given` and `answer_ms` on `study_review`, validated at the App edge against
  each deck's declared answers, with a guarded `ALTER TABLE` for existing
  databases.
- `GET /api/confusion` and the **Where you slip** panel — the Fehlerkarte of §7 —
  reachable from the shelf and from the deck header, with a one-line summary on
  the end-of-round panel.
- The palette split into gender / case / verdict families, bound to grammatical
  concepts rather than positional slots, with Nominativ reserved.
- The two defects: drop-zone hints clipped under the card, and the correct answer
  painted in the failure colour.
- `prefers-reduced-motion`, and a visible focus ring on the answer buttons.

Still open: the spatial contract (§3), the round arc (§4), the progression spine
(§5), the mastery field (§6), the social surface (§8), and the drill vocabulary
beyond the three-way swipe (§9). The leaderboard copy stays as it is — the board
is coming.

---

## Verdict

**The architecture is ahead of the interface.** The Go side has been built with
a game in mind — an append-only `study_review` log kept expressly so that
"points, streaks, daily counts and a leaderboard" stay answerable, a scheduler
that works on opaque item keys so new decks are new data, a drill registry the
client branches on, introductions that are validated structured content rather
than prose. None of that has a counterpart in the browser.

What the browser has is a competent, well-mannered SRS trainer with a swipe
skin. It is honest, it is fast, the miss choreography is genuinely thoughtful,
and it contains no game whatsoever: no streak, no score, no goal, no session
arc, no comparison to yourself or anyone else, no visible mastery. The one place
the interface makes a promise about any of this — *"This is the name other
learners will see on the leaderboard"* — points at a leaderboard that does not
exist and has no endpoint.

That is not a criticism of what was built. It is the observation that the next
increment is not "another deck." Adding decks four through twelve to the current
shelf produces a longer list of identical rectangles, and every deck added
before the foundations below are laid makes them more expensive to lay.

Two of those foundations are urgent in a way the rest are not, because they
concern data the app is discarding every day. They are in **The log is lossy**
below, and if only one section of this document gets acted on, it should be that
one.

---

## What is already right

These are load-bearing and should survive any redesign.

- **The three-way swipe is a real mechanic.** Left/up/right with the rarest
  answer on the vertical flick is a correct piece of game design, arrived at for
  the right reason. The gesture, the arrow keys, and the buttons are all the
  same input, and the card follows your finger. This is the part that already
  feels like a game.
- **The miss choreography.** A hit clears in 0.9 s; a miss holds still, reveals
  the sentence, then drifts away in the direction that would have been right,
  fading only in the back half of the journey — and any input skips the rest.
  Asymmetric hold times weighted toward the moment where learning happens is a
  pedagogical decision expressed in animation curves. Keep it exactly.
- **The whole round is local.** A batch preloads with its answers, every swipe is
  graded in the browser, one POST at the end. Nothing in the proposal below
  should be allowed to put a network round-trip between a swipe and its feedback.
- **Introductions as validated data.** A deck cannot ship without one, and a
  group cannot promise to explain an answer the deck never asks. This is the
  single best scaling decision in the codebase and it should be extended, not
  replaced.
- **Honest empty and locked states.** "Next review in 9 minutes" instead of a
  dead *Next batch* button; a locked deck shown as a distance rather than a
  closed door. Most apps get both of these wrong.
- **Colour follows position, and the introduction's groups are edged to match
  the button they lead to.** The idea is right. The specific palette is where it
  breaks, below.

---

## What breaks when the shelf gets longer

### 1. The palette is overloaded, and it already collides — done

Blue means *der*. Blue also means *Akkusativ*. Green means *das* and *Genitiv*;
rose means *die* and *Dativ*. The code is explicit that this is intentional —
the colours are treated as positional slots, first/second/third — and the
comment argues that a case deck can therefore "borrow the gender palette without
either meaning anything about the other."

That argument holds for a stylesheet. It does not hold for a learner. The noun
deck spends 213 cards teaching the association *blue = der* by pairing it with
every masculine noun in the corpus, in the introduction, on the buttons, on the
band, and in the miss list. Then the next deck reuses that same blue for a
different concept in the same visual position. Colour-coded gender is a real
convention in German classrooms precisely because it *does* stick; making it
stick and then overwriting it is worse than never using colour at all.

It also has nowhere to go. Deck four wants Nominativ (adjective endings,
relative pronouns, *Wechselpräpositionen*), and the moment a fourth answer
exists, positional slots run out. A deck that mixes gender and case on one card
— which is where this course is obviously heading, since "durch den Park"
already requires both — has no way to colour anything.

**This is the highest-leverage thing to fix, and it gets more expensive with
every deck shipped.**

### 2. The shelf is a list, not a course

Three near-identical rectangles, each with a title, a subtitle, a grey bar and a
button. It reads fine at three. At ten it is an undifferentiated scroll with no
sense of where you are, what belongs with what, or how far the whole thing goes.
Nothing on the shelf distinguishes a deck except its words: no mark, no colour,
no shape, no grouping into the strands a curriculum actually has (gender, case,
agreement, verbs).

The README says the shelf exists so that seeing what is ahead makes finishing
the deck in front of you worth doing. That intent is right and the current
rendering delivers about a tenth of it.

### 3. The HUD grows and the stage shrinks

On a 430×900 viewport, the header during play is five stacked centred lines —
title, subtitle, stats, account, deck line — occupying roughly a third of the
screen above the card. Every one of them was added for a good reason, and every
future feature (streak, goal, XP) will be added the same way, to the same
centred stack.

The title and subtitle in particular are branding shown to a signed-in learner
mid-round, who knows what app they are in. On desktop the whole layout is the
mobile column dropped into an enormous void, with the drop-zone hints stranded
1400 px from the card they annotate.

### 4. The card reserves space it never uses

The card is `min(52vh, 440px)` tall because a miss has to fit a feedback line, a
gloss, a German sentence and a translation. On every card that is *not* missed —
which is most of them, and increasingly most of them as a learner improves —
that space is empty: the word sits in the upper third with 250–400 px of blank
surface under it. It reads as an unfinished screen, and it is exactly the space a
game would use for combo state, per-card mastery, or a round rail.

### 5. The drop-zone hints sit underneath the card — done

At 430 px the card is 360 px wide and centred; the left and right hints are
pinned at `left: 18px` / `right: 18px`. The result on screen is `de` and `ie` —
both labels clipped by the card that overlaps them. On the case deck it is `akk`
and `dat`, clipped the same way. On desktop they are visible but so far from the
card as to be decorative. They are the wrong mechanism: peripheral cues belong on
the card's own edges, revealed by the drag, not in the page gutter.

### 6. On a miss, the correct answer is painted in the failure colour — done

The reveal shows **der Mann** in `--wrong` red, on a card outlined in red, while
the `der` band in the corner is blue. The one string on the screen that the
learner must encode is rendered in the colour the app uses to mean *you were
wrong*, and it contradicts the app's own gender colour in the same frame. The
red belongs on the outline and on the verdict; the answer should be in its own
colour, and should be the most legible thing on the card.

### 7. The round has no arc, and "batch" is the system's word

The end screen says **Batch complete** and *13/20 correct. 0 due now · 20/213
nouns seen.* A batch is an implementation detail — it is the name of a preloaded
array. Nothing here is framed as an achievement, a change, or a comparison: not
"better than last time," not "your best run in this deck," not "+40," not "3
days in a row." The header counter counts *down* (`20 left`), which is a
depletion, not a progress.

This screen is the single highest-value real estate in the product — it is the
moment a person decides whether to press again — and it is currently a
status report.

### 8. Accessibility floor — partly done

- No `prefers-reduced-motion` handling anywhere, and the miss animation is a
  1.6 s drift with rotation. This is the one animation in the app that a
  motion-sensitive user cannot avoid, because it fires on every mistake.
- No `:focus-visible` styling on `.choice` or `.primary`; only `.field` has a
  focus ring. The three answer buttons are the primary control and are not
  keyboard-navigable in any visible way.
- The card stack is `aria-live="polite"`, so a screen reader re-announces the
  whole card on every render rather than the verdict.

---

## The log is lossy, and the loss is permanent — done

Two pieces of information exist in the browser at the moment of an answer, are
used, and are then thrown away before anything is persisted. Both block the
features most worth building, and both are unrecoverable for every review
already recorded.

### It does not record what the learner answered

`studybus.Review` stores `Rating` — `again` / `hard` / `good` / `easy`. A miss is
`again`, and that is all that survives. The client knew that the learner swiped
*die* at *der Mann*; the server is told only that it went badly.

The consequence is that the app can never answer the question its audience most
needs answered. For someone who has finished German classes, the interesting
fact is not *how many* they got wrong — they already know they are shaky — it is
**which way they are wrong**. "You turn feminines into masculines four times as
often as the reverse." "You default to Dativ under time pressure." "You get
*durch* right when you read it and wrong when you rush it." None of that is
derivable from a rating.

This project already contains the argument for fixing it. `business/types/roleanswer`
exists, and its package comment says it plainly:

> A learner who writes "dem Mann" where "den Mann" belonged has made one of two
> completely different mistakes… Those need different help, and a single
> right/wrong on the card cannot tell them apart — so the role answer is recorded
> next to the grade.

That reasoning applies with equal force to the three-way decks that ship today.
The fix is the same shape: one column on an append-only table, `given`, holding
the answer the learner actually gave (empty for a deck where it is not
meaningful). It costs a nullable text column, one field on `gradeOutcome`, and
one field the client already has in hand.

### It does not record how long the answer took

`answer()` measures `performance.now() - state.shownAt`, uses it to pick
`easy`/`good`/`hard` through two constants, and discards the milliseconds.

Speed is the metric that matters most for this audience, and it is the one they
can move. Someone refreshing German after classes does not become *more correct*
on *der Mann* — they were already correct. They become **faster and more
certain**, and that transition from deliberate recall to automatic retrieval is
the actual goal of refresher practice. It is measurable, it improves visibly
over weeks, and it is the honest basis for both a score and a personal record.

Storing it also means the FSRS-rating thresholds (`FAST_MS = 2000`,
`SLOW_MS = 5000`) stop being two numbers picked once and never checked. With the
latencies in the log they can be fitted per learner, or at least validated.

**Recommendation: add `given` and `answer_ms` to `study_review` before the next
deck ships.** Both are additive columns on a table that is never updated. Every
day they are not there is a day of history that can never be re-derived.

---

## What "dopamine" should mean here

A note of caution, because the audience is stated and it is not the audience most
gamified language apps are built for.

These are adults who have already sat through German classes and are choosing to
come back. They are not being taught the language; they are being helped to stop
being slow at it. For that group, cartoon celebration, guilt-based streak
notifications and a mascot are as likely to read as condescension as reward.
Duolingo's loop is tuned to keep a beginner showing up at all. This audience has
already demonstrated they will show up; what they have not got is evidence that
it is working.

The reward that lands with them is **competence made legible**:

- *I am faster than I was three weeks ago* — a number that moves.
- *I have covered 87 % of the nouns in a German television episode* — a claim
  about the world, not about the app.
- *I no longer confuse these two* — a mistake visibly disappearing.
- *I remembered something I had lapsed twice* — difficulty acknowledged.

All four are dopamine. All four require the data in the previous section. And
all four are more shareable than a streak counter, because they are interesting
to people who do not use the app.

So: build the streak and the score — they work, and they cost little — but weight
the loop toward difficulty-adjusted retrieval and measurable speed, and let the
celebratory language stay dry. The app's existing voice (plain, exact, a bit
literary) is an asset. It should not turn into confetti and exclamation marks.

---

## The foundations

### 1. A colour system that survives twenty decks — done

Split the one palette into three, and separate what a colour *means* from where a
button *sits*.

**Position stays as it is.** Left and right take the two commonest answers; the
vertical flick takes the rarest. That rule is good and is independent of colour.

**Gender keeps the classroom convention** — it is already correct, and 213 cards
have taught it:

```
--gender-der   #4F8CFF   blue
--gender-die   #FF5D8F   rose
--gender-das   #37D29B   green
```

**Case gets its own family, with four reserved from the start** — Nominativ will
arrive with the first agreement deck, and reserving it now costs nothing:

```
--case-nom     #B9C2E8   steel    (reserved; deliberately unsaturated —
                                   the base form is the absence of marking)
--case-akk     #F59E0B   amber
--case-dat     #22D3EE   cyan
--case-gen     #A855F7   violet
```

Chosen to sit in the hue gaps the gender trio leaves: amber at ~38°, cyan at
~190°, violet at ~275°, against blue 215° / green 158° / rose 340°. Cyan against
green is the tightest pair and should be checked under a protanopia simulation
before it ships; if it fails, move Dativ toward #38BDF8 and Genitiv toward
#C084FC.

**Semantics get a third, separate set**, so "correct" never has to borrow an
answer colour:

```
--verdict-right  --verdict-wrong  --state-locked
--streak         --mastery-0..4
```

Two rules fall out of this, and both are worth enforcing in code:

- *A colour is bound to a grammatical concept for the life of the app.* Akkusativ
  is amber in the preposition deck, in the verb deck, in the adjective-ending
  deck, and in every mixed deck after them. The introduction's group edging
  already does the right thing; it just needs the right variable.
- *An answer's colour comes from its concept, not its slot index.* Concretely:
  replace `slot` in the `DRILLS` table with the concept key, and let the
  stylesheet map concept → colour. Same amount of code, and deck four stops
  being a colour-allocation problem.

This is a visible change for existing learners and should be shipped with a line
of explanation. It is much cheaper now than after nine more decks.

### 2. Typography with a face

The app is currently set in the system UI stack, which means it looks like a
settings screen in every screenshot. For a product whose hero content is a single
German word at 54 px, the typeface *is* the design.

Three roles, all self-hosted and subset — this is a single Go binary with
embedded assets, so a variable font subset to Latin + `ÄÖÜäöüß` is the
constraint, not a CDN link:

| Role | Face | Why this one |
|---|---|---|
| The card word, the answer buttons | **Atkinson Hyperlegible Next** | Designed to disambiguate glyphs that are easily confused. The drill's failure mode at speed is *misreading* — `Bär`/`Bar`, `schon`/`schön`, `Öl`/`Ol` — and umlaut discrimination under a 2-second target is a legibility problem before it is a memory problem. This is a functional pick, not a taste one. |
| Headings, numbers, the score | **Bricolage Grotesque** (variable, width axis) | Gives the product an actual face. The width axis lets a deck title compress on the shelf and expand on a result screen without a second family. |
| Counters, timings, timecodes | **DM Mono** | See the signature below — the corpus is film and television subtitles, and a monospaced cue register is the one type decision that comes from the material rather than from taste. |

Set a real scale (a 1.25 ratio from 13 px works with the current density) and use
weight rather than colour to carry hierarchy, so the palette above stays free to
mean grammar.

### 3. A fixed spatial contract

Stop letting the header grow. Three zones, fixed proportions, everything new goes
into an existing zone rather than onto the stack:

```
┌──────────────────────────────────────┐
│ ← Prepositions      🔥 12    340 XP  │  HUD  — one line, 44px.
├──────────────────────────────────────┤  Left: where am I + way out.
│ ▓▓▓▓▓▓▓▓▓░░░░░░░░░░░  12/20          │  Right: what I'm building.
│                                      │
│   akk ┃                      ┃ dat   │  RAIL — round progress, counts UP.
│       ┃      d u r c h       ┃       │
│       ┃    which case?       ┃       │  STAGE — the card owns the space.
│       ┃                      ┃       │  Edge cues live ON the card, not
│       ┃    ····· gen ·····   ┃       │  in the page gutter (fixes §5).
│       ┃                      ┃       │
│                                      │
├──────────────────────────────────────┤
│  [ Akkusativ ] [ Genitiv ] [ Dativ ] │  COMMIT — unchanged. It works.
└──────────────────────────────────────┘
```

- Branding (title, subtitle) belongs on the signed-out screen and the shelf, not
  in play.
- The account line belongs behind the nickname in the HUD, not beside it.
- The permanent instructional footer ("Swipe or use ← ↑ →") teaches once. Show it
  for the first round of a learner's first deck and retire it.
- **The rail counts up.** `12/20 done`, not `8 left`. Same information; one is an
  accumulation and the other is a drain.
- On desktop, the same three zones in a fixed 560 px column, centred, with the
  void filled by the mastery field (§6) rather than left empty.

### 4. Give the round an arc

A round should have a beginning, a middle with rising stakes, and an end that
reports a *change*. Four additions, none of which touch the server:

- **A combo.** Consecutive correct answers, shown on the card's own surface — the
  empty lower two-thirds from §4 is where it goes. It resets on a miss and never
  scolds. This is the single cheapest source of moment-to-moment tension and it
  costs one counter.
- **A speed band, not a stopwatch.** The app already measures latency to pick a
  rating. Show it as a subtle three-step mark (deliberate / quick / instant)
  rather than a number, so the target is *automaticity* rather than panic.
- **A closing card, not a status report.** Replace *Batch complete · 13/20
  correct* with a screen that leads with the delta:

```
        ┌─────────────────────────────┐
        │                             │
        │        + 46 XP              │   the change, first and largest
        │   your best run here        │   a comparison, when one is true
        │                             │
        │   ▓▓▓▓▓▓▓▓▓▓▓▓▓░░░  17/20   │   the score, second
        │   avg 1.4s   ↓ 0.3s         │   the metric they can move
        │                             │
        │   🔥 12 days                │   the streak, quiet
        │                             │
        │   ─────────────────────     │
        │   Worth another look        │   the corrections, unchanged.
        │   der Mann — man            │   This part is already right.
        │   …                         │
        │                             │
        │   [ Another round ]         │
        └─────────────────────────────┘
```

  The existing miss list is the best thing on that screen and should stay exactly
  as it is, below the fold of the reward.
- **Never zero-reward a miss.** A card that was missed and then read in context is
  worth a point. For this audience the correction is the product; scoring it at
  nothing teaches people to avoid hard decks.

### 5. The progression spine

All of this reads from `study_review`. No new tables; the log was kept for
exactly this.

- **Streak**, at day granularity in the learner's timezone, satisfied by a
  self-chosen daily goal rather than a single card. Ship a repair mechanic from
  day one — one free "freeze" earned per fortnight, spent automatically. Losing a
  40-day streak to a flight is the most reliable churn event in this genre, and
  the repair costs nothing to build alongside the streak but is painful to
  retrofit.
- **A daily goal the learner sets**, in three sizes, phrased in rounds rather than
  minutes — the app knows how long a round takes and the learner does not.
- **Points, difficulty-weighted.** The formula matters less than the property:
  *volume must not beat difficulty*, or the leaderboard becomes a farming
  exercise in the easiest deck. Something along the lines of

  ```
  base       again 1 · hard 4 · good 3 · easy 2      (a hard-won hit is worth
                                                      more than a reflex)
  × lapse    1 + 0.25 × min(lapses, 4)               (material that has beaten
                                                      you before pays more)
  × speed    1.0 – 1.3, from answer_ms               (needs the column above)
  ```

  Note that `easy` scores *below* `good`: a card answered instantly was already
  known, and the app should not pay most for the reviews that taught least.
  Because the log is append-only and the score is derived, this formula can be
  changed later without orphaning anyone's history — which is exactly the
  property the storage design was chosen for.
- **A weekly league leaderboard, not an all-time one.** All-time boards are won
  permanently in week three and are then demotivating for everyone else. Weekly
  reset into small cohorts keeps the top of *someone's* board reachable. This is
  also the thing the nickname panel already promises; until it ships, that copy
  should be changed.

### 6. Mastery made visible

`study_progress` already holds `State`, `Stability`, `Reps` and `Lapses` per
card, and none of it is ever shown. A deck is currently a number — *180/213 seen*
— which is the least evocative form that information could take.

Render a deck as a **field**: one small cell per card, laid out in a grid,
tinted by its gender or case colour and brightened by stability. 213 cells fit
comfortably in a phone-width block. New cards are dark, learning cards glow
faintly, mature cards are solid. A lapse visibly dims one.

This is the collection instinct, it is free from data already stored, it makes
the shelf's deck cards distinguishable at a glance (§2), and it fills the desktop
void (§3). It also gives a locked deck something to show besides a sentence about
what it is waiting for.

### 7. The signature: *die Fehlerkarte* — done

If one element is going to make this app memorable and talked about, this is the
candidate, and it is why `given` in §*The log is lossy* is urgent.

A 3×3 grid — what it was, against what you said:

```
                    you said
                der    die    das
              ┌──────┬──────┬──────┐
      der     │  ██  │  12  │   3  │
              ├──────┼──────┼──────┤
 it   die     │  31  │  ██  │   4  │      ← the story is this cell
 was          ├──────┼──────┼──────┤
      das     │   7  │   9  │  ██  │
              └──────┴──────┴──────┘

 You call feminines masculine two and a half times as often
 as the reverse. It happens most on nouns ending in -e.
```

For the case decks the same grid is Akkusativ / Dativ / Genitiv, and it is even
more diagnostic, because case errors are systematically directional in a way
gender errors are not.

Why this is the signature and not a nice-to-have:

- It tells a post-classroom learner something **they genuinely do not know about
  themselves**, which no amount of practice reveals and no teacher had time to
  measure.
- It is the honest form of "gamified pedagogy" — the game surface *is* the
  diagnostic, rather than a layer of points bolted onto one.
- It converts directly into the next round: *drill my worst cell* is one button,
  and it is exactly the interference-focused practice that helps most at this
  level.
- It is the most shareable artefact the app could produce, because it is
  interesting to a German learner who has never used it.
- Nobody else in this category has it.

It cannot be built retroactively. Every review recorded before `given` exists is
a row that can never contribute to it.

### 8. The viral surface

Be realistic about what "viral" means for a German-refresher app: not a TikTok
loop, but r/German, language Discords, teachers recommending it, and one
screenshot that makes a stranger ask what it is. Three mechanisms, in order of
how much they exploit something only this app has:

- **The coverage claim.** The corpus is *subtitle frequency*. That makes a
  genuinely novel sentence possible: *"You now know the gender of 87 % of the
  nouns in an average German television episode."* No other SRS deck can say
  that, because no other deck knows what its items are frequency-ranked against.
  Compute it, put it on the deck's completion screen, and make it the share text.
- **The Fehlerkarte as an image.** Render §7 to a canvas as a square card with
  the nickname and the app's mark. This is the artefact people post.
- **The duel.** A round is already a deterministic ordered list of items. Seed one
  from a share link — *20 cards, this deck, this order* — and a person can send a
  friend the exact round they just played and a score to beat. It needs no
  matchmaking, no realtime, and no accounts beyond the ones that exist; it is a
  URL carrying a seed and a number.

### 9. Drill vocabulary beyond the three-way swipe

The client's `DRILLS` registry is well designed and currently holds two entries
that are the same shape. The course will need at least four more shapes, and it
is worth knowing which before the layout is frozen, because each makes a
different demand on the stage:

| Shape | Needed for | Demands |
|---|---|---|
| Binary swipe (←/→) | *Wechselpräpositionen* (Wo? / Wohin?), strong vs weak verbs | Two zones, larger targets — the easiest to add |
| Four-way (←/↑/→/↓) | Adjective endings, all four cases, plural forms | The `down` direction is currently explicitly rejected in `swipeDir` |
| Two-stage card | The role-then-form decks `roleanswer` was built for | The card must survive one answer and pose a second |
| Typed production | Genuine production practice, which recognition cannot reach | A keyboard on screen — which breaks the fixed layout in §3 and needs designing for, not around |

The last one matters most for the stated audience. Recognition drills ceiling
out for people who have had classes: they can pick *der* from three options long
before they can produce it in speech. A production drill is the difference
between this being a refresher and being a tool that actually moves someone
toward C1. It also cannot share the swipe layout, so the spatial contract should
be designed knowing it is coming.

---

## Sequencing

Ordered by how much each forecloses if it is not done first, not by size.

**Before the next deck ships** — every one of these gets more expensive per deck
added, and the first two are losing data right now:

1. `given` and `answer_ms` on `study_review`, plumbed through `gradeOutcome`.
2. Split the palette; bind colour to grammatical concept, not slot index; reserve
   Nominativ.
3. Fix the two defects: hints clipped under the card, correct answer painted in
   the failure colour.
4. The spatial contract — HUD / rail / stage / commit — and retire the branding
   and the permanent footer from the play screen.
5. Accessibility floor: `prefers-reduced-motion`, `:focus-visible` on the answer
   buttons.
6. Either ship a leaderboard or change the nickname copy that promises one.

**The game layer** — none of it needs new storage:

7. Round arc: combo, rail counting up, the closing card with the delta.
8. Streak with repair, daily goal, difficulty-weighted points.
9. The mastery field, on both the deck card and the deck screen.

**The reasons to tell someone:**

10. Die Fehlerkarte (needs 1).
11. Weekly league board.
12. The coverage claim and the share image.
13. The seeded duel.

**New capability:**

14. Binary and four-way drills; the two-stage card; typed production and the
    layout it requires.

---

## Open questions

These change the proposal materially and I would rather ask than assume:

1. **Is a redesign in scope, or is this an incremental brief?** Items 1–6 are
   worth doing under either answer. §1 (palette) and §3 (spatial contract) are
   the point at which it becomes a redesign.
2. **How many decks, and of what kind?** The colour and drill proposals are sized
   for a course that eventually mixes gender and case on one card. If the plan is
   five decks that all use the existing three-way swipe, §1 and §9 are
   over-engineered.
3. **Is the audience really only post-classroom learners?** A beginner-facing
   product argues for the softer, more celebratory register I have argued
   against.
4. **Does the leaderboard go public with nicknames now?** The nickname domain was
   built with squatting and homoglyphs in mind but the README flags rename rate
   limiting as unresolved — worth closing before names are worth taking.
5. **What is the appetite for embedded webfonts?** Three subset variable faces is
   roughly 120–200 KB added to the binary and to first load. Cheap, but it is a
   real change to a project that currently ships zero external assets.
