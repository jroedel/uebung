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
  cards: [],     // {lemma, article, gloss}
  index: 0,      // current card
  results: [],   // {lemma, rating}
  shownAt: 0,    // when the current card was first shown
  locked: false, // true while an answer animates, to swallow double input
};

// --- data ------------------------------------------------------------------

async function loadBatch() {
  showStatus("Loading your cards…");
  hide(el.panel);
  try {
    const res = await fetch(API.batch(API.lang, API.limit), { headers: { Accept: "application/json" } });
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
  if (state.locked) return;
  const card = state.cards[state.index];
  if (!card) return;

  state.locked = true;
  const correct = article === card.article;
  const elapsed = performance.now() - state.shownAt;
  const rating = correct ? gradeBySpeed(elapsed) : "again";
  state.results.push({ lemma: card.lemma, rating });

  revealFeedback(correct, card);
  flingCard(DIR[card.article] || DIR[article]);

  window.setTimeout(() => {
    state.index += 1;
    state.locked = false;
    clearHints();
    if (state.index >= state.cards.length) {
      finishBatch();
    } else {
      renderStack();
    }
  }, 650);
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

loadBatch();
