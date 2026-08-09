"use strict";

// Übung der/die/das trainer — front-end.
//
// The whole point of this file is that a swipe never waits on the network. On
// load (and after each batch) it fetches ONE preloaded batch of cards, each
// carrying its correct article, and from then on every answer is graded in the
// browser with zero round-trips. Results accumulate locally and are flushed to
// the server in a single POST when the batch is done. That keeps each swipe at
// screen-refresh speed regardless of latency.

const API = {
  lang: "de",
  limit: 20,
  decks: (lang) => `/api/decks?lang=${encodeURIComponent(lang)}`,
  batch: (lang, limit, deck) =>
    `/api/batch?lang=${encodeURIComponent(lang)}&limit=${limit}&deck=${encodeURIComponent(deck)}`,
  grade: "/api/grade",
  summary: (lang, deck) =>
    `/api/summary?lang=${encodeURIComponent(lang)}&deck=${encodeURIComponent(deck)}`,
  confusion: (lang, deck) =>
    `/api/confusion?lang=${encodeURIComponent(lang)}&deck=${encodeURIComponent(deck)}`,
  me: "/auth/me",
  requestLink: "/auth/request",
  logout: "/auth/logout",
  nickname: "/auth/nickname",
  skipNickname: "/auth/nickname/skip",
};

// The drills this client knows how to put on screen, and how to draw each one.
//
// The server describes a deck's drill and says nothing about whether a browser
// can render it, which is the right division: a deck's shape is a fact about the
// deck, and what this file can draw is a fact about this file. A deck whose drill
// is not in here still appears on the shelf with its introduction — the writing is
// worth reading before the drill exists — but it cannot be started.
//
// Both drills are the same three-way swipe, and the direction each answer sits in
// is chosen the same way: the two horizontal swipes are the easy gesture, so they
// carry the two commonest answers, and the vertical flick takes the rarest. For
// gender that puts das up; for case it puts the genitive up, which is by some
// distance the least common of the three a trigger can govern.
//
// Nothing here says what an answer looks like. Each one carries its own name into
// the markup as a data-answer, and the stylesheet maps that name to a colour —
// "der" is blue because it is the masculine, "akkusativ" is amber because it is
// the accusative, and neither depends on which button it sits on. That is the
// change from the arrangement this replaced, where the colours were positional
// slots and a case deck reused the gender palette: the noun deck teaches blue =
// der over 213 cards, and the deck after it must not spend that association on
// something else. It is also what leaves room for a deck that asks for a case and
// a gender at once, since the two are drawn from different families.
const DRILLS = {
  // `short` is what the drop-zone hints say. They are peripheral cues read out of
  // the corner of the eye, so an abbreviation loses nothing, and the buttons below
  // still carry the full name.
  "article-3way": {
    prompt: "der, die, or das?",
    answers: [
      { answer: "der", dir: "left",  label: "der", short: "der" },
      { answer: "das", dir: "up",    label: "das", short: "das" },
      { answer: "die", dir: "right", label: "die", short: "die" },
    ],
  },
  "case-3way": {
    prompt: "which case?",
    answers: [
      { answer: "akkusativ", dir: "left",  label: "Akkusativ", short: "akk" },
      { answer: "genitiv",   dir: "up",    label: "Genitiv",   short: "gen" },
      { answer: "dativ",     dir: "right", label: "Dativ",     short: "dat" },
    ],
  },
};

const RENDERABLE_DRILLS = new Set(Object.keys(DRILLS));

// The swipe legend under each button, by direction.
const DIR_HINT = { left: "◀ swipe left", up: "▲ swipe up", right: "swipe right ▶" };

const SWIPE_THRESHOLD = 80; // px of travel before a drag counts as a swipe.

// drill returns the active deck's drill definition. Every render and every answer
// goes through it rather than through a hard-coded set of three articles.
function drill() {
  return (state.deck && DRILLS[state.deck.drill]) || DRILLS["article-3way"];
}

// dirFor maps an answer to the direction it flies in, and answerForDir back again.
function dirFor(answer) {
  const spec = drill().answers.find((a) => a.answer === answer);
  return spec ? spec.dir : null;
}

function answerForDir(dir) {
  const spec = drill().answers.find((a) => a.dir === dir);
  return spec ? spec.answer : null;
}

// tint paints one element in an answer's own colour by naming the answer on it.
// The stylesheet owns the mapping (see the [data-answer] rules), so this file
// never has to know that the dative is cyan.
function tint(node, answer) {
  if (answer) node.dataset.answer = answer;
}

// labelFor is how an answer is written on screen: "der" stays lowercase because
// that is how an article is written, while a case is a name and takes a capital.
function labelFor(answer) {
  const spec = drill().answers.find((a) => a.answer === answer);
  return spec ? spec.label : answer;
}

// Answer-speed thresholds (ms) that turn a correct answer into an FSRS grade.
// A miss is always "again"; a hit is graded by how quickly it came, which is a
// reasonable proxy for confidence in a recognition drill.
const FAST_MS = 2000; // under this, a correct answer is "easy".
const SLOW_MS = 5000; // over this, a correct answer is only "hard".

// How long an answered card stays on screen. These are deliberately asymmetric:
// a hit needs only long enough to confirm you were right, while a miss is the
// one moment in the whole round where learning can actually happen, so it gets
// room to be read.
//
// A missed card holds still first — the article has to land before anything
// moves — and only then travels, slowly, in the direction that would have been
// correct. The fade is delayed in CSS to the back half of that journey so the
// answer stays legible while it goes. (Before this, the card faded out in 280ms
// while the answer was still fading in over 150ms, which left about a quarter
// second of readable text and then 370ms of empty stage.)
//
// None of this is a floor on your pace: any input during a reveal skips the
// rest of it, so knowing the answer still lets you move at speed.
const HIT_HOLD_MS = 900;   // total on-screen time for a correct answer.
const MISS_HOLD_MS = 500;  // still, fully opaque, while the answer registers.
const MISS_FLING_MS = 1100; // slow travel; must match .card.slow-exit in the CSS.

const el = {
  deck: document.getElementById("deck"),
  controls: document.getElementById("controls"),
  stats: document.getElementById("stats"),
  statRemaining: document.getElementById("stat-remaining"),
  statLearned: document.getElementById("stat-learned"),
  statDeck: document.getElementById("stat-deck"),
  statDue: document.getElementById("stat-due"),
  panel: document.getElementById("panel"),
  panelTitle: document.getElementById("panel-title"),
  panelBody: document.getElementById("panel-body"),
  account: document.getElementById("account"),
  accountEmail: document.getElementById("account-email"),
  accountNickname: document.getElementById("account-nickname"),
  renameBtn: document.getElementById("rename-btn"),
  signoutBtn: document.getElementById("signout-btn"),
  nicknamePanel: document.getElementById("nickname-panel"),
  nicknameTitle: document.getElementById("nickname-title"),
  nicknameBody: document.getElementById("nickname-body"),
  nicknameForm: document.getElementById("nickname-form"),
  nicknameInput: document.getElementById("nickname-input"),
  nicknameBtn: document.getElementById("nickname-btn"),
  nicknameSkipLine: document.getElementById("nickname-skip-line"),
  nicknameSkipBtn: document.getElementById("nickname-skip-btn"),
  nicknameCancelLine: document.getElementById("nickname-cancel-line"),
  nicknameCancelBtn: document.getElementById("nickname-cancel-btn"),
  signinPanel: document.getElementById("signin-panel"),
  signinForm: document.getElementById("signin-form"),
  signinEmail: document.getElementById("signin-email"),
  signinBtn: document.getElementById("signin-btn"),
  signinBody: document.getElementById("signin-body"),
  misses: document.getElementById("misses"),
  missList: document.getElementById("miss-list"),
  againBtn: document.getElementById("again-btn"),
  panelDecksBtn: document.getElementById("panel-decks-btn"),
  statusPanel: document.getElementById("status-panel"),
  statusText: document.getElementById("status-text"),
  decksPanel: document.getElementById("decks-panel"),
  deckList: document.getElementById("deck-list"),
  deckLine: document.getElementById("deck-line"),
  deckTitle: document.getElementById("deck-title"),
  decksBtn: document.getElementById("decks-btn"),
  rereadBtn: document.getElementById("reread-btn"),
  introPanel: document.getElementById("intro-panel"),
  introHeading: document.getElementById("intro-heading"),
  introBody: document.getElementById("intro-body"),
  introGroups: document.getElementById("intro-groups"),
  introClosing: document.getElementById("intro-closing"),
  introStartBtn: document.getElementById("intro-start-btn"),
  introBackBtn: document.getElementById("intro-back-btn"),
  slipsBtn: document.getElementById("slips-btn"),
  slipsPanel: document.getElementById("slips-panel"),
  slipsHeading: document.getElementById("slips-heading"),
  slipsIntro: document.getElementById("slips-intro"),
  slipsGrid: document.getElementById("slips-grid"),
  slipsInsight: document.getElementById("slips-insight"),
  slipsCount: document.getElementById("slips-count"),
  slipsBackBtn: document.getElementById("slips-back-btn"),
  slipLine: document.getElementById("slip-line"),
  slipSummary: document.getElementById("slip-summary"),
  slipMoreBtn: document.getElementById("slip-more-btn"),
  footer: document.getElementById("footer"),
  hintBox: document.getElementById("hints"),
  hints: {
    left: document.querySelector(".hint-left"),
    up: document.querySelector(".hint-up"),
    right: document.querySelector(".hint-right"),
  },
};

const state = {
  decks: [],     // the shelf, as /api/decks describes it
  deck: null,    // the deck being studied, or null on the shelf
  cards: [],     // {item, answer, gloss, example, example_en, phrase?, note?}
  index: 0,      // current card
  results: [],   // {item, rating}
  shownAt: 0,    // when the current card was first shown
  locked: false, // true while an answer animates, to swallow double input
  advance: null, // while a reveal is on screen: run it early to skip the rest
  timers: [],    // pending reveal timeouts, cleared if the reveal is cut short
  suggestion: "", // the nickname currently offered on the skip button
};

// --- stale page recovery -----------------------------------------------------

// This release is the first to serve the client with an ETag, which means it is
// also the last one that can meet a page cached without one. Every earlier build
// sent index.html with no ETag, no Last-Modified and no Cache-Control, so a
// browser was free to keep it and pair it with a freshly fetched app.js — and this
// script reaches for elements that page does not contain. Unguarded, that is a
// TypeError on the first null and a white screen.
//
// So: check for something only the current page has, and if it is missing, reload
// past the cache. A query string is a different cache key, which is the reliable
// way to make a browser go and ask. Once reloaded, the page carries an ETag and
// this can never fire again.
// Every id here is one this release's page has and some earlier page did not, so
// the list grows with each release that adds markup app.js reaches for. Missing
// any of them means the browser has paired an old page with this script, and
// wire() would throw on the first null before start() ever ran.
const CURRENT_PAGE_MARKERS = ["decks-panel", "deck-list", "intro-panel", "hints", "slips-panel"];
const RELOAD_FLAG = "uebung-reloaded-past-cache";

// recoverIfStale returns true when it has taken over — the caller must stop.
function recoverIfStale() {
  if (CURRENT_PAGE_MARKERS.every((id) => document.getElementById(id))) return false;

  // Only once. If the reload lands on the same stale page the cache is beyond
  // reach from here, and looping would be worse than saying so.
  let alreadyTried = true;
  try {
    alreadyTried = sessionStorage.getItem(RELOAD_FLAG) === "1";
    if (!alreadyTried) sessionStorage.setItem(RELOAD_FLAG, "1");
  } catch (err) {
    // Private browsing can throw on sessionStorage. Reloading blind risks a
    // loop, so treat it as already tried and show the message instead.
    console.error(err);
  }

  if (alreadyTried) {
    document.body.textContent =
      "This page is an old cached copy. Please reload with Ctrl-Shift-R (Cmd-Shift-R on a Mac).";

    return true;
  }

  location.replace(`${location.pathname}?fresh=${Date.now()}`);

  return true;
}

// --- sign-in ---------------------------------------------------------------

// start decides what the visitor sees. Every study endpoint now needs a session,
// so asking who we are has to come before asking for cards -- otherwise the first
// thing a signed-out visitor gets is a failed batch request.
async function start() {
  hide(el.panel);

  let me = null;
  try {
    const res = await fetch(API.me, { headers: { Accept: "application/json" } });
    if (res.ok) me = await res.json();
  } catch (err) {
    console.error(err);
    showStatus("Couldn't reach the server. Check your connection and try again.");

    return;
  }

  if (!me) {
    showSignIn();

    return;
  }

  // Show the address. A session can be started by following a link someone else
  // sent, so "which account am I in" has to be answerable at a glance.
  el.accountEmail.textContent = me.email;
  showAccount(me);
  show(el.account);

  // A learner with no name yet gets asked before the deck, because this is the
  // one moment they are already stopped and reading. It is a prompt and not a
  // gate: skipping takes the suggested name and carries straight on.
  if (!me.nickname) {
    showNicknamePrompt(me.suggestion);

    return;
  }

  showShelf();
}

// showAccount renders the header's identity line. The nickname leads because it
// is the public one; "Change" appears only once there is something to change.
function showAccount(me) {
  el.accountNickname.textContent = me.nickname || "";
  el.renameBtn.hidden = !me.nickname;
}

// showSignIn presents the email form. The expired case is flagged by the auth
// callback, which redirects here with ?signin=expired rather than reporting why a
// link failed -- expired, already used and forged all look the same on purpose.
function showSignIn() {
  hideEverything();
  hide(el.account);

  if (new URLSearchParams(location.search).get("signin") === "expired") {
    el.signinBody.textContent =
      "That sign-in link didn\u2019t work \u2014 they expire quickly and only work once. Here\u2019s a fresh one.";
  }

  show(el.signinPanel);
  el.signinEmail.focus();
}

async function requestLink(e) {
  e.preventDefault();

  const address = el.signinEmail.value.trim();
  if (!address) return;

  el.signinBtn.disabled = true;
  el.signinBtn.textContent = "Sending\u2026";

  try {
    const res = await fetch(API.requestLink, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: address }),
    });

    if (res.status === 400) {
      const body = await res.json().catch(() => ({}));
      el.signinBody.textContent = body.error || "That doesn\u2019t look like an email address.";
      el.signinBtn.disabled = false;
      el.signinBtn.textContent = "Email me a link";

      return;
    }

    // Anything else is treated as sent. The server answers the same way for a
    // known address, an unknown one and a rate-limited one, so that no one can
    // use this form to discover who has an account -- and the client must not
    // undo that by reporting the difference.
    el.signinForm.hidden = true;
    el.signinBody.textContent =
      `If ${address} can receive mail, a sign-in link is on its way. It works once and expires in 15 minutes.`;
  } catch (err) {
    console.error(err);
    el.signinBody.textContent = "Couldn\u2019t reach the server. Check your connection and try again.";
    el.signinBtn.disabled = false;
    el.signinBtn.textContent = "Email me a link";
  }
}

// --- nickname ---------------------------------------------------------------

// showNicknamePrompt asks for a display name.
//
// suggestion is the name the server would assign if the learner skips, and it is
// shown on the skip button rather than described in the abstract: "Rather not
// choose? Be Blaue Eule" is a decision someone can make at a glance, where
// "skip" alone asks them to accept an unknown.
//
// The suggestion is not reserved. If it has been taken by the time they press
// the button, the server quietly assigns a different one — which is why the
// assigned name is always read back from the response rather than assumed.
function showNicknamePrompt(suggestion) {
  hideEverything();

  el.nicknameTitle.textContent = "Pick a nickname";
  el.nicknameBody.textContent =
    "This is the name other learners will see on the leaderboard — so it need not be your real one.";
  el.nicknameInput.value = "";
  hide(el.nicknameCancelLine); // there is nothing to go back to yet.

  // No suggestion means the server could not produce one just now. The prompt
  // still works; only the skip offer goes away, since there would be no name to
  // put on it.
  if (suggestion) {
    el.nicknameSkipBtn.textContent = `Be “${suggestion}”`;
    // Held so the button can claim the name it is showing. Reading it back off
    // the label would work but would tie the request to the button's wording.
    state.suggestion = suggestion;
    show(el.nicknameSkipLine);
  } else {
    state.suggestion = "";
    hide(el.nicknameSkipLine);
  }

  show(el.nicknamePanel);
  el.nicknameInput.focus();
}

// openRename is what the header's "Change" does.
//
// Opening the panel clears the card stack, and answers live in the browser until
// the batch ends, so anything already answered is sent first. Renaming should
// never cost a learner their round — and the flush is cheap, since it is the
// same request the end of a batch makes anyway.
async function openRename() {
  if (state.results.length) {
    await flush();
    state.results = [];
  }

  showRename(el.accountNickname.textContent);
}

// showRename reuses the same panel for changing an existing name. There is no
// skip here: the name is already set, and "skip" would have nothing to mean.
// There is a cancel instead, because unlike the first-time prompt this panel was
// opened deliberately and has somewhere to go back to.
function showRename(current) {
  hideEverything();
  hide(el.nicknameSkipLine);
  show(el.nicknameCancelLine);

  el.nicknameTitle.textContent = "Change your nickname";
  el.nicknameBody.textContent = "Other learners see this name on the leaderboard.";
  el.nicknameInput.value = current || "";

  show(el.nicknamePanel);
  el.nicknameInput.focus();
  el.nicknameInput.select();
}

async function submitNickname(e) {
  e.preventDefault();

  const name = el.nicknameInput.value.trim();
  if (!name) return;

  el.nicknameBtn.disabled = true;
  el.nicknameBtn.textContent = "Saving…";

  try {
    const res = await fetch(API.nickname, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ nickname: name }),
    });

    if (res.status === 401) {
      showSignIn();

      return;
    }

    // 400 is a name that breaks the rules, 409 one that someone else already
    // has. Both are the learner's to fix and both carry a message written to be
    // read, so neither is worth paraphrasing here.
    if (res.status === 400 || res.status === 409) {
      const body = await res.json().catch(() => ({}));
      el.nicknameBody.textContent = body.error || "That name can’t be used — try another.";
      resetNicknameButton();
      el.nicknameInput.focus();
      el.nicknameInput.select();

      return;
    }

    if (!res.ok) throw new Error(`nickname failed: ${res.status}`);

    finishNickname(await res.json());
  } catch (err) {
    console.error(err);
    el.nicknameBody.textContent = "Couldn’t reach the server. Check your connection and try again.";
    resetNicknameButton();
  }
}

async function skipNickname() {
  el.nicknameSkipBtn.disabled = true;

  try {
    // Send the name on the button. The server claims exactly this one unless it
    // has been taken since it was offered, in which case it picks another.
    const res = await fetch(API.skipNickname, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ nickname: state.suggestion }),
    });

    if (res.status === 401) {
      showSignIn();

      return;
    }
    if (!res.ok) throw new Error(`skip failed: ${res.status}`);

    // Read the name back rather than trusting the one on the button: the server
    // assigns a different one if the suggestion was taken in the meantime.
    finishNickname(await res.json());
  } catch (err) {
    console.error(err);
    el.nicknameBody.textContent = "Couldn’t reach the server. Check your connection and try again.";
    el.nicknameSkipBtn.disabled = false;
  }
}

// finishNickname closes the panel and carries on into the deck.
function finishNickname(me) {
  showAccount(me);
  show(el.account);

  hide(el.nicknamePanel);
  resetNicknameButton();
  el.nicknameSkipBtn.disabled = false;

  showShelf();
}

function resetNicknameButton() {
  el.nicknameBtn.disabled = false;
  el.nicknameBtn.textContent = "Save";
}

// --- the shelf ---------------------------------------------------------------

// hideEverything clears the stage so a screen can be shown without inheriting
// whatever was up before it. Every screen calls this first, which is why none of
// them has to know which screen it is replacing.
function hideEverything() {
  clearTimers();
  clearStack();
  state.advance = null;
  state.locked = false;

  hide(el.statusPanel);
  hide(el.panel);
  hide(el.signinPanel);
  hide(el.nicknamePanel);
  hide(el.decksPanel);
  hide(el.introPanel);
  hide(el.slipsPanel);
  hide(el.controls);
  hide(el.stats);
  hide(el.hintBox);
  hide(el.footer);
  hide(el.deckLine);
}

// showShelf is the home screen: fetch the catalog and put it on screen.
//
// It always refetches rather than reusing what it has. Everything that sends you
// back here — finishing a round, changing your name, backing out of a deck —
// changed something the shelf displays, and a stale bar or a deck that should
// have just unlocked would be the first thing anyone noticed.
async function showShelf() {
  hideEverything();
  state.deck = null;
  delete document.body.dataset.drill;
  showStatus("Loading your decks…");

  let decks = null;
  try {
    const res = await fetch(API.decks(API.lang), { headers: { Accept: "application/json" } });
    if (res.status === 401) {
      showSignIn();

      return;
    }
    if (!res.ok) throw new Error(`decks failed: ${res.status}`);
    decks = (await res.json()).decks || [];
  } catch (err) {
    console.error(err);
    showStatus("Couldn't reach the server. Check your connection and try again.");

    return;
  }

  state.decks = decks;
  renderShelf();

  hide(el.statusPanel);
  show(el.decksPanel);
}

// renderShelf draws one card per deck.
//
// A locked deck is drawn in full — title, subtitle, and the bar showing how far
// through its prerequisite you are. Hiding what comes next would remove the only
// reason to finish what you are on, so the lock is shown as a distance rather
// than as a closed door.
function renderShelf() {
  el.deckList.replaceChildren();

  for (const deck of state.decks) {
    el.deckList.appendChild(buildDeckCard(deck));
  }
}

function buildDeckCard(deck) {
  const item = document.createElement("li");
  item.className = "deck-card";

  const playable = RENDERABLE_DRILLS.has(deck.drill);
  const open = deck.unlocked && playable;
  if (!deck.unlocked) item.classList.add("locked");
  else if (!playable) item.classList.add("soon");

  const title = document.createElement("h3");
  title.className = "deck-card-title";
  title.textContent = deck.title;

  const subtitle = document.createElement("p");
  subtitle.className = "deck-card-subtitle";
  subtitle.textContent = deck.subtitle;

  item.append(title, subtitle);

  if (deck.unlocked) {
    // An open deck reports its own progress; the bar is over the deck itself.
    item.appendChild(progressBar(deck.learned, deck.deck_size));

    const line = document.createElement("p");
    line.className = "deck-card-stat";

    // Default to the working state, and override the two that read wrongly without
    // it: an untouched deck has no progress to report, and a deck with nothing due
    // says "0 due now" — which looks like a deck that has run out rather than one
    // resting between reviews. When we know the moment it reopens, say that instead.
    let stat = `${deck.learned}/${deck.deck_size} seen · ${deck.due_now} due now`;
    const reopens = deck.due_now === 0 ? describeDue(deck.next_due) : "";

    if (deck.learned === 0) {
      stat = `${deck.deck_size} cards · not started`;
    } else if (reopens) {
      stat = `${deck.learned}/${deck.deck_size} seen · next review ${reopens}`;
    }

    line.textContent = stat;
    item.appendChild(line);
  } else {
    // A locked deck's bar is over the deck that gates it, because that is the
    // one the learner can actually move.
    item.appendChild(progressBar(deck.requires_seen, deck.requires_need));

    const line = document.createElement("p");
    line.className = "deck-card-stat";
    line.textContent =
      `Locked — ${deck.requires_seen}/${deck.requires_need} cards of “${deck.requires_title}” seen`;
    item.appendChild(line);
  }

  const actions = document.createElement("div");
  actions.className = "deck-card-actions";

  if (open) {
    const study = document.createElement("button");
    study.className = "primary deck-card-study";
    study.type = "button";
    study.textContent = deck.learned === 0 ? "Start" : "Study";
    study.addEventListener("click", () => openDeck(deck));
    actions.appendChild(study);
  } else if (deck.unlocked && !playable) {
    const soon = document.createElement("span");
    soon.className = "deck-card-soon";
    soon.textContent = "Drill coming soon";
    actions.appendChild(soon);
  }

  // The introduction is readable whatever the deck's state. It is the part that
  // is worth something on its own — a locked deck you have read about is a deck
  // you have already started learning.
  const read = document.createElement("button");
  read.className = "linkish";
  read.type = "button";
  read.textContent = "How this deck works";
  read.addEventListener("click", () => showIntro(deck));
  actions.appendChild(read);

  // The error profile, offered only once there is any history to profile. On an
  // untouched deck it would be a link to an empty grid, which is a worse first
  // impression than no link at all.
  if (deck.learned > 0) {
    const slips = document.createElement("button");
    slips.className = "linkish";
    slips.type = "button";
    slips.textContent = "Where you slip";
    slips.addEventListener("click", () => showSlips(deck, "shelf"));
    actions.appendChild(slips);
  }

  item.appendChild(actions);

  return item;
}

// progressBar renders have/need as a filled track. A need of 0 fills it: there is
// nothing to do, which is complete rather than empty.
function progressBar(have, need) {
  const track = document.createElement("div");
  track.className = "bar";

  const fill = document.createElement("div");
  fill.className = "bar-fill";
  const share = need > 0 ? Math.min(1, have / need) : 1;
  fill.style.width = `${Math.round(share * 100)}%`;

  track.appendChild(fill);

  return track;
}

// openDeck is what pressing Start does. The introduction comes first the one time
// it has not been read, because arriving at a wall of prepositions with no idea
// what governs them is the situation the introduction exists to prevent.
function openDeck(deck) {
  if (deck.intro_seen) {
    startDeck(deck);

    return;
  }

  showIntro(deck);
}

// showIntro puts a deck's introduction on screen. It is reachable from the shelf
// for any deck and from the header while studying one, so the offer to start is
// only made when starting is actually possible.
function showIntro(deck) {
  hideEverything();

  el.introHeading.textContent = deck.intro.heading;

  el.introBody.replaceChildren();
  for (const paragraph of deck.intro.body) {
    const p = document.createElement("p");
    p.textContent = paragraph;
    el.introBody.appendChild(p);
  }

  el.introGroups.replaceChildren();
  for (const group of deck.intro.groups) {
    const li = document.createElement("li");
    li.className = "intro-group";
    // The group is coloured to match the button it leads to, so the explanation
    // and the drill look like one thing rather than two.
    if (group.answer) li.dataset.answer = group.answer;

    const label = document.createElement("p");
    label.className = "intro-group-label";
    label.textContent = group.label;

    const members = document.createElement("p");
    members.className = "intro-group-members";
    members.lang = "de";
    members.textContent = group.members;

    const hook = document.createElement("p");
    hook.className = "intro-group-hook";
    hook.textContent = group.hook;

    li.append(label, members, hook);
    el.introGroups.appendChild(li);
  }

  el.introClosing.textContent = deck.intro.closing;

  const canStart = deck.unlocked && RENDERABLE_DRILLS.has(deck.drill);
  el.introStartBtn.hidden = !canStart;
  if (canStart) el.introStartBtn.onclick = () => startDeck(deck);

  show(el.introPanel);
}

// --- where you slip -----------------------------------------------------------

// MIN_SLIPS is how many recorded answers a deck needs before the profile says
// anything about a pattern.
//
// A grid built from nine answers has a largest cell, and naming it would be
// telling someone about their German on the strength of two mistakes. The grid
// itself is drawn whatever the count — seeing it fill up is the point — but the
// sentence that interprets it waits until it is worth trusting.
const MIN_SLIPS = 30;

// answerSpecs returns a deck's answers as the drill defines them, or, for a deck
// this build cannot draw, a plain list built from the deck's own answer names.
//
// The profile is worth reading for a deck whose drill has not shipped yet — the
// server counts answers regardless of what can render them — so this degrades to
// the raw names rather than refusing.
function answerSpecs(deck, answers) {
  const spec = DRILLS[deck.drill];
  if (spec) return answers.map((a) => spec.answers.find((s) => s.answer === a) || { answer: a, label: a, short: a });

  return answers.map((a) => ({ answer: a, label: a, short: a }));
}

// showSlips draws a deck's error profile.
//
// from is where "back" should return to: the shelf, or the deck being studied.
// The panel is reachable from both, and a learner who opened it mid-round should
// not be dumped out of the round to close it.
async function showSlips(deck, from) {
  hideEverything();
  el.slipsHeading.textContent = "Where you slip";
  el.slipsIntro.textContent = deck.title;
  el.slipsGrid.replaceChildren();
  el.slipsInsight.textContent = "";
  el.slipsCount.textContent = "Loading…";
  show(el.slipsPanel);

  el.slipsBackBtn.textContent = from === "deck" ? "Back to the deck" : "Back to all decks";
  el.slipsBackBtn.onclick = () => (from === "deck" ? startDeck(deck) : showShelf());

  let profile = null;
  try {
    const res = await fetch(API.confusion(API.lang, deck.id), { headers: { Accept: "application/json" } });
    if (res.status === 401) {
      showSignIn();

      return;
    }
    if (!res.ok) throw new Error(`confusion failed: ${res.status}`);
    profile = await res.json();
  } catch (err) {
    console.error(err);
    el.slipsCount.textContent = "Couldn’t load your answers. Check your connection and try again.";

    return;
  }

  renderSlips(deck, profile);
}

// renderSlips draws the grid and the sentence under it.
function renderSlips(deck, profile) {
  const specs = answerSpecs(deck, profile.answers || []);
  const cells = profile.cells || [];

  el.slipsGrid.replaceChildren();

  // Nothing recorded yet. Say which of the two reasons it is, because they call
  // for opposite things: a learner who has never opened the deck should go and
  // study, and one with months of history behind them is looking at a feature
  // that started counting after they did.
  if (!profile.recorded) {
    el.slipsCount.textContent = deck.learned > 0
      ? "Your earlier rounds were answered before this deck started recording which answer you gave. From now on they count."
      : "Nothing here yet — answer a round and this fills in.";

    return;
  }

  const head = document.createElement("tr");
  head.appendChild(cornerCell());
  for (const spec of specs) {
    const th = document.createElement("th");
    th.className = "slips-col";
    th.scope = "col";
    th.textContent = spec.short;
    tint(th, spec.answer);
    head.appendChild(th);
  }
  el.slipsGrid.appendChild(head);

  specs.forEach((rowSpec, i) => {
    const tr = document.createElement("tr");

    const rowHead = document.createElement("th");
    rowHead.className = "slips-row";
    rowHead.scope = "row";
    rowHead.textContent = rowSpec.label;
    tint(rowHead, rowSpec.answer);
    tr.appendChild(rowHead);

    const total = (cells[i] || []).reduce((sum, n) => sum + n, 0);

    specs.forEach((colSpec, j) => {
      const td = document.createElement("td");
      const n = (cells[i] || [])[j] || 0;
      td.textContent = n || "·";

      // The diagonal is where the answer was right. It is shown rather than
      // blanked because it is what turns a count of slips into a rate: four
      // die-for-der mistakes mean something different over ten feminine cards
      // than over four hundred.
      if (i === j) {
        td.className = "slips-cell slips-hit";
      } else {
        td.className = "slips-cell";
        // Weight by how much of this row went astray this particular way, so a
        // busy row does not simply look worse than a quiet one.
        if (n > 0) td.style.setProperty("--slip-weight", String(Math.min(1, n / Math.max(1, total))));
        if (n > 0) tint(td, colSpec.answer);
      }

      tr.appendChild(td);
    });

    // The row's own accuracy, which is the reading most people want first.
    const rate = document.createElement("td");
    rate.className = "slips-rate";
    rate.textContent = total > 0 ? `${Math.round(((cells[i] || [])[i] || 0) / total * 100)}%` : "—";
    tr.appendChild(rate);

    el.slipsGrid.appendChild(tr);
  });

  el.slipsCount.textContent = `${profile.recorded} answer${profile.recorded === 1 ? "" : "s"} recorded in this deck.`;
  el.slipsInsight.textContent = slipInsight(specs, cells, profile.recorded);
}

// cornerCell is the empty top-left of the grid, which carries the two axis labels
// because a matrix with unlabelled axes can be read backwards.
function cornerCell() {
  const th = document.createElement("th");
  th.className = "slips-corner";
  th.scope = "col";

  const wanted = document.createElement("span");
  wanted.className = "slips-axis-row";
  wanted.textContent = "it was";

  const said = document.createElement("span");
  said.className = "slips-axis-col";
  said.textContent = "you said";

  th.append(said, wanted);

  return th;
}

// biggestSlip finds the largest off-diagonal cell: the mistake this learner makes
// most. Returns null when there is none.
function biggestSlip(specs, cells) {
  let best = null;

  specs.forEach((_, i) => {
    specs.forEach((__, j) => {
      if (i === j) return;
      const n = (cells[i] || [])[j] || 0;
      if (n > 0 && (!best || n > best.count)) best = { row: i, col: j, count: n };
    });
  });

  return best;
}

// slipInsight is the sentence under the grid: what the grid says, in words.
//
// It names the pair and then its mirror, because the direction is the whole
// point. "You confuse der and die" is something every learner already suspects;
// "you call feminines masculine four times as often as the reverse" is a fact
// about this person that they cannot get any other way, and it says which half of
// the pair to actually work on.
function slipInsight(specs, cells, recorded) {
  if (recorded < MIN_SLIPS) return "Keep going — a few more rounds and this will show which way you tend to go wrong.";

  const worst = biggestSlip(specs, cells);
  if (!worst) return "No mistakes recorded in this deck. That is not nothing.";

  const wanted = specs[worst.row].label;
  const said = specs[worst.col].label;
  const mirror = (cells[worst.col] || [])[worst.row] || 0;

  const lead = `Your commonest slip: “${wanted}” answered ${said}, ${worst.count} time${worst.count === 1 ? "" : "s"}.`;

  if (mirror === 0) return `${lead} The reverse has never happened.`;

  const ratio = worst.count / mirror;
  if (ratio < 1.5) return `${lead} It goes the other way about as often, so the pair is the problem rather than one side of it.`;

  return `${lead} The reverse happened ${mirror} time${mirror === 1 ? "" : "s"} — you go this way ${ratio.toFixed(1)}× as often.`;
}

// showSlipSummary puts one line about the profile on the end-of-round panel.
//
// It is fetched after the flush, so it already includes the round just played.
// A failure is silent: this is an aside on a panel whose job is to report the
// round, and it must never be the reason that panel looks broken.
async function showSlipSummary(deck) {
  hide(el.slipLine);
  if (!deck) return;

  try {
    const res = await fetch(API.confusion(API.lang, deck.id), { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(`confusion failed: ${res.status}`);

    const profile = await res.json();
    if (!profile.recorded || profile.recorded < MIN_SLIPS) return;

    const specs = answerSpecs(deck, profile.answers || []);
    const worst = biggestSlip(specs, profile.cells || []);
    if (!worst) return;

    el.slipSummary.textContent =
      `Across every round: “${specs[worst.row].label}” answered ${specs[worst.col].label} ${worst.count} times.`;
    el.slipMoreBtn.onclick = () => showSlips(deck, "deck");
    show(el.slipLine);
  } catch (err) {
    console.error(err);
  }
}

// startDeck opens a deck for study.
function startDeck(deck) {
  hideEverything();

  state.deck = deck;

  // The drill is put on the body so the stylesheet can size for it. A case deck
  // answers with words rather than three-letter articles, and asks with whole
  // constructions rather than single nouns, so type that fits "der" does not fit
  // "Akkusativ" — and that is a question for the stylesheet, not for this file.
  document.body.dataset.drill = deck.drill;

  el.deckTitle.textContent = deck.title;
  show(el.deckLine);

  loadSummary();
  loadBatch();
}

// leaveDeck goes back to the shelf, sending anything already answered first.
// Answers live in the browser until a round ends, so leaving mid-round would
// otherwise throw them away — and the flush is the same request the end of the
// round would have made anyway.
async function leaveDeck() {
  if (state.results.length) {
    await flush();
    state.results = [];
  }

  showShelf();
}

// --- data ------------------------------------------------------------------

async function loadBatch() {
  showStatus("Loading your cards…");
  hide(el.panel);
  hide(el.signinPanel);
  hide(el.nicknamePanel);
  hide(el.decksPanel);
  hide(el.introPanel);
  // Drop any reveal still in flight, so a timer from the last batch cannot
  // advance an index that now points into a fresh set of cards.
  clearTimers();
  state.advance = null;
  state.locked = false;
  try {
    const res = await fetch(API.batch(API.lang, API.limit, deckID()), { headers: { Accept: "application/json" } });
    if (res.status === 401) {
      showSignIn();

      return;
    }
    // The gate is the server's to enforce, so it can refuse a deck the shelf
    // thought was open — a stale shelf, or a deck opened in another tab and since
    // re-locked. Say what it is waiting for rather than showing a generic failure.
    if (res.status === 403) {
      finishLocked(await res.json().catch(() => null));
      return;
    }
    if (!res.ok) throw new Error(`batch failed: ${res.status}`);
    const data = await res.json();
    state.cards = data.cards || [];
    state.index = 0;
    state.results = [];
    if (state.cards.length === 0) {
      finishEmpty(data.next_due);
      return;
    }
    renderControls();
    hide(el.statusPanel);
    show(el.controls);
    show(el.stats);
    show(el.hintBox);
    show(el.footer);
    renderStack();
  } catch (err) {
    showStatus("Couldn't reach the server. Check your connection and try again.");
    console.error(err);
  }
}

// The header's learned/deck/due counters otherwise arrive only in the grade
// response at the end of a batch, which leaves them showing their placeholder
// dashes for a learner's whole first session. Fetch them once at start-up so the
// header is populated on arrival.
//
// Deliberately not awaited by the caller: these counters are decorative, and the
// cards must not wait on them. A failure is logged and left at the placeholders.
async function loadSummary() {
  try {
    const res = await fetch(API.summary(API.lang, deckID()), { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(`summary failed: ${res.status}`);
    applySummary(await res.json());
  } catch (err) {
    console.error(err);
  }
}

async function flush() {
  // Send the whole batch's grades in one request, naming the deck they belong to
  // — without it the server would file every answer against the noun deck.
  try {
    const res = await fetch(API.grade, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ lang: API.lang, deck: deckID(), results: state.results }),
    });
    if (!res.ok) throw new Error(`grade failed: ${res.status}`);
    return await res.json();
  } catch (err) {
    console.error(err);
    return null;
  }
}

// deckID is the deck currently being studied. Empty when none is open, which the
// server reads as the noun deck — the only deck there was when the parameter did
// not exist.
function deckID() {
  return state.deck ? state.deck.id : "";
}

// --- rendering -------------------------------------------------------------

function renderStack() {
  el.deck.innerHTML = "";

  const next = state.cards[state.index + 1];
  if (next) el.deck.appendChild(buildCard(next, true));

  const current = state.cards[state.index];
  if (current) {
    const card = buildCard(current, false);
    el.deck.appendChild(card);
    attachDrag(card, current);
    state.shownAt = performance.now();
  }

  updateStats();
}

// buildCard draws one question. The bands are the three answers in their colour
// slots, revealed on the answer; the prompt and the word itself come from the
// deck's drill, so a preposition is asked exactly the way a noun is.
function buildCard(data, behind) {
  const card = document.createElement("div");
  card.className = "card" + (behind ? " behind" : "");

  // Each band names its own answer, which is what gives it its colour, and its own
  // direction, which is what puts it in a corner. Two attributes rather than one
  // class because the two facts are independent: an answer keeps its colour if it
  // is ever moved to a different swipe.
  const bands = drill().answers
    .map((a) => `<div class="band" data-answer="${escapeHtml(a.answer)}" data-dir="${a.dir}">${escapeHtml(a.label)}</div>`)
    .join("");

  card.innerHTML = `
    ${bands}
    <p class="lemma">${escapeHtml(data.item)}</p>
    <p class="prompt">${escapeHtml(drill().prompt)}</p>
    <div class="feedback"></div>
    <div class="gloss"></div>
    <p class="example" lang="de"></p>
    <p class="example-en" lang="en"></p>
  `;
  return card;
}

// renderControls redraws the three answer buttons and the drop-zone hints for the
// active drill. They are markup in index.html for the noun deck's sake — the page
// should not be blank before the first batch arrives — and rewritten here as soon
// as a deck is opened, because their labels are the deck's answers and nothing
// else can know them.
function renderControls() {
  const spec = drill();

  el.controls.replaceChildren();
  for (const a of spec.answers) {
    const btn = document.createElement("button");
    btn.className = "choice";
    btn.type = "button";
    btn.dataset.answer = a.answer;
    btn.textContent = a.label;

    const legend = document.createElement("small");
    legend.textContent = DIR_HINT[a.dir];
    btn.appendChild(legend);

    el.controls.appendChild(btn);
  }

  for (const a of spec.answers) {
    const hint = el.hints[a.dir];
    if (hint) {
      hint.textContent = a.short;
      tint(hint, a.answer);
    }
  }
}

function updateStats() {
  const remaining = Math.max(0, state.cards.length - state.index);
  el.statRemaining.textContent = remaining;
}

function applySummary(sum) {
  if (!sum) return;
  el.statLearned.textContent = sum.learned;
  el.statDeck.textContent = sum.deck_size;
  el.statDue.textContent = sum.due_now;
}

// --- answering -------------------------------------------------------------

function answer(given) {
  // A press while an answer is on screen means "I've got it" — skip the rest of
  // the reveal instead of swallowing the input. Without this the longer miss
  // reveal would be a tax on every miss rather than a floor for the ones that
  // need it.
  if (state.locked) {
    if (state.advance) state.advance();
    return;
  }

  const card = state.cards[state.index];
  if (!card) return;

  state.locked = true;
  const correct = given === card.answer;
  const elapsed = performance.now() - state.shownAt;
  const rating = correct ? gradeBySpeed(elapsed) : "again";

  // What was answered and how long it took travel with the grade.
  //
  // Both were measured here and thrown away: the rating collapses a miss to
  // "again" whatever was swiped, and the milliseconds were used to pick between
  // easy, good and hard and then dropped. Neither is recoverable afterwards — the
  // log only grows — and between them they are the two things a learner at this
  // level actually wants to know. Which way they are wrong is what the error
  // profile is built from; how fast they answer is the number that moves as
  // recall becomes automatic.
  state.results.push({
    item: card.item,
    rating,
    given,
    // Whole milliseconds, non-negative: the server refuses a negative time, and a
    // fractional one would be false precision on a person pressing a button.
    answer_ms: Math.max(0, Math.round(elapsed)),
  });

  revealFeedback(correct, card);

  const direction = dirFor(card.answer) || dirFor(given);
  if (correct) {
    flingCard(direction);
    scheduleAdvance(HIT_HOLD_MS);
    return;
  }

  // Missed: show the sentence, hold still so the article can be read, then leave
  // slowly. The card is only told to move once the hold is over, so the slow
  // transition applies to the whole journey.
  revealExample(card);
  markSlowExit();
  after(() => flingCard(direction), MISS_HOLD_MS);
  scheduleAdvance(MISS_HOLD_MS + MISS_FLING_MS);
}

// after runs fn later and records the timer so a skipped reveal can cancel it.
function after(fn, delay) {
  state.timers.push(window.setTimeout(fn, delay));
}

// scheduleAdvance arms the move to the next card and exposes it as state.advance
// so an impatient learner can trigger it early. It is idempotent: whichever of
// the timer or the early call lands first, the other becomes a no-op.
function scheduleAdvance(delay) {
  state.advance = () => {
    clearTimers();
    state.advance = null;
    state.index += 1;
    state.locked = false;
    clearHints();
    if (state.index >= state.cards.length) {
      finishBatch();
    } else {
      renderStack();
    }
  };

  after(() => state.advance && state.advance(), delay);
}

function clearTimers() {
  for (const t of state.timers) window.clearTimeout(t);
  state.timers = [];
}

function gradeBySpeed(ms) {
  if (ms < FAST_MS) return "easy";
  if (ms > SLOW_MS) return "hard";
  return "good";
}

function revealFeedback(correct, card) {
  const top = el.deck.lastElementChild;
  if (!top) return;
  top.classList.add("answered", correct ? "correct" : "incorrect");
  const fb = top.querySelector(".feedback");
  const gloss = top.querySelector(".gloss");

  const said = answerLine(card);
  fb.textContent = correct ? `Richtig — ${said}` : said;
  gloss.textContent = card.gloss;

  // The card takes the correct answer's colour, which is what the feedback line
  // is drawn in. On a miss that line used to be red — the failure colour, on a
  // card already outlined in red, contradicting the answer's own colour on the
  // band beside it. The outline says how it went; the answer says what it is.
  tint(top, card.answer);

  const band = top.querySelector(`.band[data-answer="${cssEscape(card.answer)}"]`);
  if (band) band.style.opacity = "1";
}

// answerLine is the correct answer as a learner should read it back.
//
// For a noun that is the article in front of the noun — "die Zeit" — which is the
// form worth memorising. For a trigger it is the declined phrase, "durch den
// Park", because the case is learned as a shape and not as a label; the label
// alone would be the answer to a quiz rather than to the language. The phrase
// carries the case name after it so the two are tied together.
function answerLine(card) {
  if (card.phrase) return `${card.phrase} — ${labelFor(card.answer)}`;

  return `${card.answer} ${card.item}`;
}

// revealExample puts the missed noun's sentence on the card itself. It is already
// on the card data — the batch preloads it — so this costs no request, and it
// turns the pause into the correction rather than dead waiting time. The same
// sentence appears again in the end-of-round list.
function revealExample(card) {
  const top = el.deck.lastElementChild;
  if (!top) return;

  const de = top.querySelector(".example");
  const en = top.querySelector(".example-en");
  if (!de || !en) return;

  // For a noun the sentence uses it in a natural case, so its article may be
  // declined and need not be the one being drilled; the nominative follows for
  // reference. For a trigger the sentence is already the point and the phrase has
  // just been shown as the answer, so nothing is appended.
  de.textContent = card.phrase ? card.example : `${card.example} (${card.answer} ${card.item})`;
  en.textContent = card.example_en;
  top.classList.add("show-example");
}

// markSlowExit switches the top card to the slow, late-fading transition used
// for misses. It must be set before the transform is applied.
function markSlowExit() {
  const top = el.deck.lastElementChild;
  if (top) top.classList.add("slow-exit");
}

function flingCard(direction) {
  const top = el.deck.lastElementChild;
  if (!top) return;
  const off = { left: "translateX(-140%)", right: "translateX(140%)", up: "translateY(-140%)" }[direction];
  top.style.transform = `${off} rotate(${direction === "left" ? -12 : direction === "right" ? 12 : 0}deg)`;
  top.style.opacity = "0";
}

// --- gestures --------------------------------------------------------------

function attachDrag(card, data) {
  let startX = 0, startY = 0, dragging = false;

  const down = (e) => {
    if (state.locked) return;
    dragging = true;
    const p = point(e);
    startX = p.x;
    startY = p.y;
    card.style.transition = "none";
    card.setPointerCapture && e.pointerId != null && card.setPointerCapture(e.pointerId);
  };

  const move = (e) => {
    if (!dragging) return;
    const p = point(e);
    const dx = p.x - startX;
    const dy = p.y - startY;
    card.style.transform = `translate(${dx}px, ${dy}px) rotate(${dx / 18}deg)`;
    highlightDir(dominantDir(dx, dy));
  };

  const up = (e) => {
    if (!dragging) return;
    dragging = false;
    card.style.transition = "";
    const p = point(e);
    const dx = p.x - startX;
    const dy = p.y - startY;
    const dir = swipeDir(dx, dy);
    if (dir) {
      answer(answerForDir(dir));
    } else {
      card.style.transform = ""; // snap back
      clearHints();
    }
  };

  card.addEventListener("pointerdown", down);
  card.addEventListener("pointermove", move);
  card.addEventListener("pointerup", up);
  card.addEventListener("pointercancel", up);
}

function point(e) {
  return { x: e.clientX, y: e.clientY };
}

function dominantDir(dx, dy) {
  if (Math.abs(dx) > Math.abs(dy)) return dx < 0 ? "left" : "right";
  return dy < 0 ? "up" : "down";
}

function swipeDir(dx, dy) {
  const dir = dominantDir(dx, dy);
  const dist = dir === "up" || dir === "down" ? Math.abs(dy) : Math.abs(dx);
  if (dist < SWIPE_THRESHOLD) return null;
  if (dir === "down") return null; // down is not a valid answer
  return dir;
}

function highlightDir(dir) {
  clearHints();
  if (dir === "left") el.hints.left.classList.add("active");
  else if (dir === "right") el.hints.right.classList.add("active");
  else if (dir === "up") el.hints.up.classList.add("active");
}

function clearHints() {
  el.hints.left.classList.remove("active");
  el.hints.right.classList.remove("active");
  el.hints.up.classList.remove("active");
}

// --- batch completion ------------------------------------------------------

async function finishBatch() {
  hide(el.controls);
  hide(el.hintBox);
  hide(el.footer);
  clearStack();
  showStatus("Saving…");
  const sum = await flush();
  hide(el.statusPanel);
  applySummary(sum);

  const correct = state.results.filter((r) => r.rating !== "again").length;
  el.panelTitle.textContent = "Batch complete";

  // "Next batch" is worth offering only when there is one. A deck with nothing due
  // and nothing unseen has none — pressing it would fetch an empty batch and land
  // on the caught-up panel — and the summary we just flushed knows both facts, so
  // the button gives way to the time the deck reopens. Both conditions are needed:
  // nothing due while cards remain unseen still has a batch to serve.
  const exhausted = Boolean(sum) && sum.due_now === 0 && sum.learned >= sum.deck_size;
  const reopens = exhausted ? describeDue(sum.next_due) : "";

  if (exhausted) hide(el.againBtn);
  else show(el.againBtn);

  let tail = sum ? `${sum.due_now} due now · ${sum.learned}/${sum.deck_size} nouns seen.` : "";
  if (reopens) tail += ` Your next review is ${reopens}.`;

  el.panelBody.textContent = `${correct}/${state.results.length} correct. ${tail}`.trim();
  renderMisses();
  show(el.panel);

  // The round is reported from what is already in hand; the pattern across every
  // round needs the server. Deliberately not awaited — the panel is complete
  // without it, and a slow or failed request must not hold up the one screen the
  // learner is waiting on.
  showSlipSummary(state.deck);
}

// Every card answered wrong this round, in the order it came up. A miss is
// exactly a result rated "again" — the rating answer() assigns when the swipe did
// not match the card's answer — so this needs no separate bookkeeping.
function missedCards() {
  const byItem = new Map(state.cards.map((c) => [c.item, c]));
  return state.results
    .filter((r) => r.rating === "again")
    .map((r) => byItem.get(r.item))
    .filter(Boolean);
}

// Show each missed card in a sentence, so the round ends on the material that
// needs the work rather than on a score alone.
//
// The heading is the answer as it is worth memorising — "die Zeit" for a noun,
// "durch den Park" for a trigger — and the sentence follows. For a noun the
// sentence uses it in a natural case, so its article may be declined ("Ich kenne
// den Mann nicht.") and is not necessarily the one being drilled; the nominative
// is appended for reference, built at render time so it always matches the gender
// this deck teaches. A trigger needs no such gloss: its phrase is already the
// heading.
function renderMisses() {
  const missed = missedCards();
  el.missList.replaceChildren();

  if (missed.length === 0) {
    hide(el.misses);
    return;
  }

  for (const card of missed) {
    const item = document.createElement("li");

    const head = document.createElement("div");
    const word = document.createElement("span");
    word.className = "miss-word";
    tint(word, card.answer);
    word.textContent = card.phrase ? card.phrase : `${card.answer} ${card.item}`;
    const gloss = document.createElement("span");
    gloss.className = "miss-gloss";
    gloss.textContent = ` — ${card.gloss}`;
    head.append(word, gloss);

    const example = document.createElement("p");
    example.className = "miss-example";
    example.lang = "de";
    example.textContent = `${card.example} `;

    const aside = document.createElement("span");
    aside.className = "miss-nominative";
    aside.textContent = card.phrase ? `(${labelFor(card.answer)})` : `(${card.answer} ${card.item})`;
    example.appendChild(aside);

    const translation = document.createElement("p");
    translation.className = "miss-translation";
    translation.lang = "en";
    translation.textContent = card.example_en;

    item.append(head, example, translation);

    // A card with an authored aside gets it here, where there is room to read it
    // — the drill itself is no place for a sentence of prose.
    if (card.note) {
      const note = document.createElement("p");
      note.className = "miss-note";
      note.textContent = card.note;
      item.appendChild(note);
    }

    el.missList.appendChild(item);
  }

  show(el.misses);
}

// finishEmpty is the panel for a deck with nothing to do.
//
// It hides "Next batch" rather than offering it. The button asks the server for
// another batch, and the server has just said there is none — pressing it refetches
// the same empty answer and redraws this same panel, which reads as a broken button
// rather than as a finished deck. The only honest actions here are to leave, and to
// know when to come back.
function finishEmpty(nextDue) {
  hide(el.controls);
  hide(el.hintBox);
  hide(el.footer);
  clearStack();
  hide(el.statusPanel);
  hide(el.againBtn);
  el.panelTitle.textContent = "All caught up";

  const when = describeDue(nextDue);
  el.panelBody.textContent = when
    ? `Nothing is due in this deck right now. Your next review is ${when}.`
    : "Nothing is due in this deck right now. Come back later.";

  hide(el.misses); // nothing was answered, so any list from a previous round is stale.
  show(el.panel);
}

// finishLocked is the panel for a deck the server will not open yet. It should be
// unreachable from the shelf, which draws the same lock — so it exists for the
// cases the shelf cannot know about, and it explains rather than just refusing.
function finishLocked(info) {
  hide(el.controls);
  hide(el.hintBox);
  hide(el.footer);
  clearStack();
  hide(el.statusPanel);
  hide(el.againBtn);
  hide(el.misses);

  el.panelTitle.textContent = "Not open yet";
  el.panelBody.textContent = info && info.requires_need
    ? `Work through ${info.requires_need} cards of the deck before this one to unlock it — you have ${info.requires_seen}.`
    : "This deck unlocks once you have worked through the one before it.";

  show(el.panel);
}

// describeDue renders a scheduling instant as the phrase a learner actually wants:
// how long until the deck reopens, in their own timezone, at the coarsest unit that
// is still useful. The server sends UTC and holds no opinion about where it is read;
// all of the localising happens here.
//
// An absent or unparseable value returns the empty string, so every caller can treat
// "we do not know when" as one case rather than rendering "Invalid Date" at a
// learner.
function describeDue(iso) {
  if (!iso) return "";

  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return "";

  const ms = when.getTime() - Date.now();
  if (ms <= 0) return "now";

  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return rtf.format(minutes, "minute");

  const hours = Math.round(minutes / 60);
  if (hours < 24) return rtf.format(hours, "hour");

  // "tomorrow" rather than "in 1 day", which is what numeric:"auto" buys.
  return rtf.format(Math.round(hours / 24), "day");
}

// --- helpers ---------------------------------------------------------------

function clearStack() { el.deck.innerHTML = ""; }
function show(node) { node.hidden = false; }
function hide(node) { node.hidden = true; }
function showStatus(text) { el.statusText.textContent = text; show(el.statusPanel); }

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

// cssEscape quotes a value for use inside a selector. Answers are authored data
// rather than anything a learner types, so nothing here is currently exotic — but
// a selector built from data is worth escaping on principle rather than on
// inspection of today's decks.
function cssEscape(s) {
  return window.CSS && CSS.escape ? CSS.escape(String(s)) : String(s).replace(/["\\]/g, "\\$&");
}

// wire binds every listener and is the last thing that runs.
//
// It is a function rather than statements at the end of the file so that
// recoverIfStale can be consulted first. Half of what it reaches for does not
// exist on a page from a previous release, and `null.addEventListener` would
// throw here — before start() ever ran, so the recovery would never get its
// chance. Nothing below may assume more than the check above verified.
function wire() {
  // Keyboard: ← der, ↑ das, → die. Only while a deck is open — otherwise an arrow
  // key pressed on the shelf or in the introduction would grade a card that is not
  // on screen.
  document.addEventListener("keydown", (e) => {
    if (!state.deck) return;

    // The arrow keys mean directions, and the deck's drill says what each
    // direction answers — so the same three keys drill genders in one deck and
    // cases in another without the keyboard needing to know which.
    const dir = { ArrowLeft: "left", ArrowUp: "up", ArrowRight: "right" }[e.key];
    if (!dir) return;

    const given = answerForDir(dir);
    if (given) {
      e.preventDefault();
      answer(given);
    }
  });

  // Tap buttons.
  el.controls.addEventListener("click", (e) => {
    const btn = e.target.closest(".choice");
    if (btn) answer(btn.dataset.answer);
  });

  el.againBtn.addEventListener("click", loadBatch);
  el.panelDecksBtn.addEventListener("click", leaveDeck);

  // The way back to the shelf, from the header while studying and from the intro.
  el.decksBtn.addEventListener("click", leaveDeck);
  el.introBackBtn.addEventListener("click", leaveDeck);

  // Rereading the introduction mid-deck. Like renaming, it clears the stack, so
  // any answers held in the browser are sent before they can be lost.
  el.rereadBtn.addEventListener("click", async () => {
    const deck = state.deck;
    if (!deck) return;

    if (state.results.length) {
      await flush();
      state.results = [];
    }

    showIntro(deck);
  });

  // The profile, from the header while studying. Like rereading the introduction
  // it clears the stack, so anything answered but not yet sent goes first — and
  // sending it means the grid the learner is about to look at includes the round
  // they are in the middle of.
  el.slipsBtn.addEventListener("click", async () => {
    const deck = state.deck;
    if (!deck) return;

    if (state.results.length) {
      await flush();
      state.results = [];
    }

    showSlips(deck, "deck");
  });

  el.signinForm.addEventListener("submit", requestLink);

  el.nicknameForm.addEventListener("submit", submitNickname);
  el.nicknameSkipBtn.addEventListener("click", skipNickname);
  el.renameBtn.addEventListener("click", openRename);
  // Cancelling returns to the shelf rather than restoring the round: the stack was
  // cleared to show the panel, and the answers it held were already sent.
  el.nicknameCancelBtn.addEventListener("click", () => {
    hide(el.nicknamePanel);
    showShelf();
  });

  el.signoutBtn.addEventListener("click", async () => {
    try {
      await fetch(API.logout, { method: "POST" });
    } catch (err) {
      console.error(err);
    }
    // Reload rather than patching state: the whole page is now signed out, and a
    // fresh start() is the one path that decides what to show.
    location.assign("/");
  });
}

// The entry point. The stale-page check comes before everything, because on a
// cached page from an earlier release there is nothing here worth attempting.
if (!recoverIfStale()) {
  wire();
  start();
}
