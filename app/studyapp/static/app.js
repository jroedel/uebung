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
  batch: (lang, limit) => `/api/batch?lang=${encodeURIComponent(lang)}&limit=${limit}`,
  grade: "/api/grade",
  summary: (lang) => `/api/summary?lang=${encodeURIComponent(lang)}`,
  me: "/auth/me",
  requestLink: "/auth/request",
  logout: "/auth/logout",
};

// Direction → article. Left = der, Up = das, Right = die. Chosen so the two
// horizontal swipes (the most common gesture) cover the two most frequent
// genders, with the vertical flick for neuter.
const DIR = { der: "left", das: "up", die: "right" };
const SWIPE_THRESHOLD = 80; // px of travel before a drag counts as a swipe.

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
  signoutBtn: document.getElementById("signout-btn"),
  signinPanel: document.getElementById("signin-panel"),
  signinForm: document.getElementById("signin-form"),
  signinEmail: document.getElementById("signin-email"),
  signinBtn: document.getElementById("signin-btn"),
  signinBody: document.getElementById("signin-body"),
  misses: document.getElementById("misses"),
  missList: document.getElementById("miss-list"),
  againBtn: document.getElementById("again-btn"),
  statusPanel: document.getElementById("status-panel"),
  statusText: document.getElementById("status-text"),
  hints: {
    left: document.querySelector(".hint-left"),
    up: document.querySelector(".hint-up"),
    right: document.querySelector(".hint-right"),
  },
};

const state = {
  cards: [],     // {lemma, article, gloss, example, example_en}
  index: 0,      // current card
  results: [],   // {lemma, rating}
  shownAt: 0,    // when the current card was first shown
  locked: false, // true while an answer animates, to swallow double input
  advance: null, // while a reveal is on screen: run it early to skip the rest
  timers: [],    // pending reveal timeouts, cleared if the reveal is cut short
};

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
  show(el.account);

  loadSummary();
  loadBatch();
}

// showSignIn presents the email form. The expired case is flagged by the auth
// callback, which redirects here with ?signin=expired rather than reporting why a
// link failed -- expired, already used and forged all look the same on purpose.
function showSignIn() {
  hide(el.statusPanel);
  hide(el.account);
  hide(el.controls);
  hide(el.stats);
  clearStack();

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

// --- data ------------------------------------------------------------------

async function loadBatch() {
  showStatus("Loading your cards…");
  hide(el.panel);
  hide(el.signinPanel);
  // Drop any reveal still in flight, so a timer from the last batch cannot
  // advance an index that now points into a fresh set of cards.
  clearTimers();
  state.advance = null;
  state.locked = false;
  try {
    const res = await fetch(API.batch(API.lang, API.limit), { headers: { Accept: "application/json" } });
    if (res.status === 401) {
      showSignIn();

      return;
    }
    if (!res.ok) throw new Error(`batch failed: ${res.status}`);
    const data = await res.json();
    state.cards = data.cards || [];
    state.index = 0;
    state.results = [];
    if (state.cards.length === 0) {
      finishEmpty();
      return;
    }
    hide(el.statusPanel);
    show(el.controls);
    show(el.stats);
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
    const res = await fetch(API.summary(API.lang), { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(`summary failed: ${res.status}`);
    applySummary(await res.json());
  } catch (err) {
    console.error(err);
  }
}

async function flush() {
  // Send the whole batch's grades in one request.
  try {
    const res = await fetch(API.grade, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ lang: API.lang, results: state.results }),
    });
    if (!res.ok) throw new Error(`grade failed: ${res.status}`);
    return await res.json();
  } catch (err) {
    console.error(err);
    return null;
  }
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

function buildCard(data, behind) {
  const card = document.createElement("div");
  card.className = "card" + (behind ? " behind" : "");
  card.innerHTML = `
    <div class="band der">der</div>
    <div class="band die">die</div>
    <div class="band das">das</div>
    <p class="lemma">${escapeHtml(data.lemma)}</p>
    <p class="prompt">der, die, or das?</p>
    <div class="feedback"></div>
    <div class="gloss"></div>
    <p class="example" lang="de"></p>
    <p class="example-en" lang="en"></p>
  `;
  return card;
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

function answer(article) {
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
  const correct = article === card.article;
  const elapsed = performance.now() - state.shownAt;
  const rating = correct ? gradeBySpeed(elapsed) : "again";
  state.results.push({ lemma: card.lemma, rating });

  revealFeedback(correct, card);

  const direction = DIR[card.article] || DIR[article];
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
  fb.textContent = correct ? `Richtig — ${card.article} ${card.lemma}` : `${card.article} ${card.lemma}`;
  gloss.textContent = card.gloss;
  const band = top.querySelector(`.band.${card.article}`);
  if (band) band.style.opacity = "1";
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

  // The sentence uses the noun in a natural case, so its article may be declined
  // and need not be the one being drilled; the nominative follows for reference.
  de.textContent = `${card.example} (${card.article} ${card.lemma})`;
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
      answer(articleForDir(dir));
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

function articleForDir(dir) {
  return Object.keys(DIR).find((a) => DIR[a] === dir);
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
  clearStack();
  showStatus("Saving…");
  const sum = await flush();
  hide(el.statusPanel);
  applySummary(sum);

  const correct = state.results.filter((r) => r.rating !== "again").length;
  el.panelTitle.textContent = "Batch complete";
  el.panelBody.textContent = `${correct}/${state.results.length} correct. ` +
    (sum ? `${sum.due_now} due now · ${sum.learned}/${sum.deck_size} nouns seen.` : "");
  renderMisses();
  show(el.panel);
}

// Every noun answered wrong this round, in the order it came up. A miss is
// exactly a result rated "again" — the rating answer() assigns when the swipe
// did not match the card's article — so this needs no separate bookkeeping.
function missedCards() {
  const byLemma = new Map(state.cards.map((c) => [c.lemma, c]));
  return state.results
    .filter((r) => r.rating === "again")
    .map((r) => byLemma.get(r.lemma))
    .filter(Boolean);
}

// Show each missed noun in a sentence, so the round ends on the words that need
// the work rather than on a score alone.
//
// The sentence uses the noun in a natural case, so its article may be declined
// ("Ich kenne den Mann nicht.") and is not necessarily the one being drilled.
// The nominative is therefore composed here from the card's own article and
// lemma and appended for reference — built at render time rather than stored, so
// it always matches the gender this deck teaches.
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
    word.className = `miss-word ${card.article}`;
    word.textContent = `${card.article} ${card.lemma}`;
    const gloss = document.createElement("span");
    gloss.className = "miss-gloss";
    gloss.textContent = ` — ${card.gloss}`;
    head.append(word, gloss);

    const example = document.createElement("p");
    example.className = "miss-example";
    example.lang = "de";
    example.textContent = `${card.example} `;
    const nominative = document.createElement("span");
    nominative.className = "miss-nominative";
    nominative.textContent = `(${card.article} ${card.lemma})`;
    example.appendChild(nominative);

    const translation = document.createElement("p");
    translation.className = "miss-translation";
    translation.lang = "en";
    translation.textContent = card.example_en;

    item.append(head, example, translation);
    el.missList.appendChild(item);
  }

  show(el.misses);
}

function finishEmpty() {
  hide(el.controls);
  clearStack();
  hide(el.statusPanel);
  el.panelTitle.textContent = "All caught up";
  el.panelBody.textContent = "Nothing is due right now. Come back later, or start another batch.";
  hide(el.misses); // nothing was answered, so any list from a previous round is stale.
  show(el.panel);
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

// Keyboard: ← der, ↑ das, → die.
document.addEventListener("keydown", (e) => {
  const map = { ArrowLeft: "der", ArrowUp: "das", ArrowRight: "die" };
  const article = map[e.key];
  if (article) {
    e.preventDefault();
    answer(article);
  }
});

// Tap buttons.
el.controls.addEventListener("click", (e) => {
  const btn = e.target.closest(".choice");
  if (btn) answer(btn.dataset.article);
});

el.againBtn.addEventListener("click", loadBatch);

el.signinForm.addEventListener("submit", requestLink);

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

start();
