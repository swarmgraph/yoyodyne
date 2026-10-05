#!/usr/bin/env node
// Renders the dashboard page from the fixtures, without a browser, and writes
// what each scenario leaves in the DOM.
//
// The page's script is the one thing that turns the read model into the seven
// sections, and a Go test cannot run it. This runs it under Node against a small
// document model — enough of the DOM for the script's own needs and nothing
// more — with fetch answered from the fixtures under ./fixtures, and serializes
// the page each scenario ends in. TestThePageRendersEverySectionInEveryState in
// page_test.go runs this and holds the output to the renders under ./renders,
// which are the evidence a reviewer is handed for each section in each state;
// -update-renders rewrites them.
//
// The document model is deliberately small. The script uses getElementById,
// createElement, textContent, appendChild, removeChild, firstChild, className,
// setAttribute, removeAttribute, addEventListener (on an element and on the
// document), focus, isConnected, and value, and this implements those and no more, so a
// new DOM call in the script fails here loudly rather than passing on a shim
// that quietly did nothing. It is not a browser: layout, style, and the policy
// are checked elsewhere, and what this checks is that the right words land in
// the right places.
//
// A scenario can also act on the page once it is drawn: open a grouping or an
// item's card by clicking the element that carries its key, and press Escape.
// That is how the pop-ups reach each of their states, from the same fixtures.
// Such a scenario's render is the pop-ups it left open and nothing else: the
// page beneath them is another scenario's render already, named in the file,
// and a copy of it under every pop-up would be the same page once per pop-up
// scenario — which is more than a reviewer is handed at once, so a change
// carrying it could not be approved.
//
// Usage: node render.js --out <directory>
//        TZ=UTC node render.js --check
// Writes <directory>/<scenario>.html for every scenario below — the document
// as the page's script left it, with the one page state and the one state per
// panel the stylesheet would show, the hidden ones dropped, and a pop-up kept
// only while it is open — and a <directory>/matrix.json saying which state
// each section and each pop-up reached in each.
// --check compares every render to ./renders without writing files.

"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");
const assert = require("assert");

const here = __dirname;
const assets = path.join(here, "..", "assets");

// ---- a small document model ---------------------------------------------------

const voidTags = new Set(["meta", "link", "input", "br", "hr", "img"]);

function escapeText(text) {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttribute(text) {
  return escapeText(text).replace(/"/g, "&quot;");
}

class Node {
  constructor(document) {
    this.ownerDocument = document;
    this.parentNode = null;
    this.childNodes = [];
  }
  get firstChild() {
    return this.childNodes.length ? this.childNodes[0] : null;
  }
  get isConnected() {
    let node = this;
    while (node.parentNode) {
      node = node.parentNode;
    }
    return node === this.ownerDocument.root;
  }
  appendChild(child) {
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    child.parentNode = this;
    this.childNodes.push(child);
    return child;
  }
  removeChild(child) {
    const at = this.childNodes.indexOf(child);
    if (at === -1) {
      throw new Error("removeChild: not a child");
    }
    this.childNodes.splice(at, 1);
    child.parentNode = null;
    return child;
  }
}

class Text extends Node {
  constructor(document, data) {
    super(document);
    this.data = data;
  }
  get textContent() {
    return this.data;
  }
  serialize() {
    return escapeText(this.data);
  }
}

class Element extends Node {
  constructor(document, tagName) {
    super(document);
    this.tagName = tagName.toLowerCase();
    this.attributes = new Map();
    this.listeners = {};
    this.value = "";
    this.focused = false;
  }
  get id() {
    return this.attributes.get("id") || "";
  }
  get className() {
    return this.attributes.get("class") || "";
  }
  set className(value) {
    this.attributes.set("class", String(value));
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === "id") {
      this.ownerDocument.index();
    }
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  removeAttribute(name) {
    this.attributes.delete(name);
  }
  get textContent() {
    return this.childNodes.map((child) => child.textContent).join("");
  }
  set textContent(value) {
    this.childNodes.forEach((child) => { child.parentNode = null; });
    this.childNodes = [];
    if (value !== "") {
      this.appendChild(new Text(this.ownerDocument, String(value)));
    }
  }
  addEventListener(name, listener) {
    (this.listeners[name] = this.listeners[name] || []).push(listener);
  }
  dispatch(name, event) {
    (this.listeners[name] || []).forEach((listener) => listener(event));
  }
  focus() {
    this.focused = true;
  }
  serialize(indent) {
    const pad = indent || "";
    const attributes = [...this.attributes.entries()]
      .map(([name, value]) => (value === "" ? ` ${name}` : ` ${name}="${escapeAttribute(value)}"`))
      .join("");
    if (voidTags.has(this.tagName)) {
      return `${pad}<${this.tagName}${attributes}>`;
    }
    const inline = this.childNodes.every((child) => child instanceof Text);
    if (inline) {
      return `${pad}<${this.tagName}${attributes}>${this.childNodes.map((child) => child.serialize()).join("")}</${this.tagName}>`;
    }
    const inner = this.childNodes
      .map((child) => (child instanceof Text ? (child.data.trim() ? pad + "  " + escapeText(child.data.trim()) : null) : child.serialize(pad + "  ")))
      .filter((line) => line !== null)
      .join("\n");
    return `${pad}<${this.tagName}${attributes}>\n${inner}\n${pad}</${this.tagName}>`;
  }
}

class Document {
  constructor() {
    this.root = null;
    this.byId = new Map();
    this.listeners = {};
  }
  createElement(tagName) {
    return new Element(this, tagName);
  }
  addEventListener(name, listener) {
    (this.listeners[name] = this.listeners[name] || []).push(listener);
  }
  dispatch(name, event) {
    (this.listeners[name] || []).forEach((listener) => listener(event));
  }
  index() {
    this.byId = new Map();
    const walk = (node) => {
      if (node instanceof Element) {
        if (node.id) {
          this.byId.set(node.id, node);
        }
        node.childNodes.forEach(walk);
      }
    };
    if (this.root) {
      walk(this.root);
    }
  }
  getElementById(id) {
    this.index();
    return this.byId.get(id) || null;
  }
  find(predicate) {
    const found = [];
    const walk = (node) => {
      if (node instanceof Element) {
        if (predicate(node)) {
          found.push(node);
        }
        node.childNodes.forEach(walk);
      }
    };
    walk(this.root);
    return found;
  }
}

// parse is enough of an HTML parser for the shell: tags, attributes, text, the
// void elements, and comments, with no error recovery. The shell is ours and is
// well formed; anything this cannot parse is a mistake in the shell.
function parse(html) {
  const document = new Document();
  const root = new Element(document, "#document");
  document.root = root;
  const stack = [root];
  const tag = /<!--[\s\S]*?-->|<!doctype[^>]*>|<\/([a-zA-Z][\w-]*)\s*>|<([a-zA-Z][\w-]*)((?:\s+[\w-]+(?:="[^"]*")?)*)\s*(\/?)>/g;
  let last = 0;
  let match;
  while ((match = tag.exec(html)) !== null) {
    const text = html.slice(last, match.index);
    if (text) {
      stack[stack.length - 1].appendChild(new Text(document, text));
    }
    last = tag.lastIndex;
    const [whole, closing, opening, attributes, selfClosing] = match;
    if (whole.startsWith("<!")) {
      continue;
    }
    if (closing) {
      const top = stack.pop();
      if (top.tagName !== closing.toLowerCase()) {
        throw new Error(`unbalanced </${closing}> against <${top.tagName}>`);
      }
      continue;
    }
    const element = new Element(document, opening);
    const attribute = /([\w-]+)(?:="([^"]*)")?/g;
    let pair;
    while ((pair = attribute.exec(attributes || "")) !== null) {
      element.attributes.set(pair[1], pair[2] === undefined ? "" : pair[2].replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&"));
    }
    stack[stack.length - 1].appendChild(element);
    if (!voidTags.has(element.tagName) && !selfClosing) {
      stack.push(element);
    }
  }
  if (stack.length !== 1) {
    throw new Error(`unclosed <${stack[stack.length - 1].tagName}>`);
  }
  document.index();
  return document;
}

// ---- the scenarios ----------------------------------------------------------------

function fixture(name) {
  return JSON.parse(fs.readFileSync(path.join(here, "fixtures", name + ".json"), "utf8"));
}

// An answer is what fetch resolves with for one path: a body and a status, or
// "pending" for a fetch that never answers, or "unreachable" for one that rejects.
function ok(body) {
  return { status: 200, ok: true, body };
}

function refused(status, error) {
  return { status, ok: false, body: { error }, statusText: status === 503 ? "Service Unavailable" : "Unauthorized" };
}

// aged is a reading as the dashboard serves it, with the snapshot it was
// served from beside its own fields.
function aged(reading, snapshot) {
  return Object.assign({ snapshot: Object.assign({ taken_at: "2026-09-18T14:05:00Z", age_seconds: 4, interval_seconds: 10, stale: false }, snapshot) }, reading);
}

const pending = { pending: true };
const unreachable = { unreachable: true };

// items answers /api/items/<id> for the ids named, from the item fixtures.
function items(...ids) {
  const answers = {};
  ids.forEach((id) => { answers["/api/items/" + id] = ok(fixture("item-" + id)); });
  return answers;
}

// reports answers /api/program-managers/<agent> for the names given, from the
// report fixtures.
function reports(...agents) {
  const answers = {};
  agents.forEach((agent) => { answers["/api/program-managers/" + agent] = ok(fixture("report-" + agent)); });
  return answers;
}

// A scenario's `open` is what a reader clicks once the page is drawn, in
// order: a grouping by its key, an item by its id, or a program manager's
// report by its name — or `poll`, which is
// the page asking again in between, redrawing every section under the
// pop-up; `escape` presses Escape afterwards. The pop-ups answer from the standing and the throughput already
// in hand, and the card from the item answers. `beneath` names the scenario
// whose render is the page under the pop-ups, which is what the render of one
// of these leaves out; over() is such a scenario, made from the one beneath.
function over(name, beneath, steps) {
  const page = pages.find((scenario) => scenario.name === beneath);
  return Object.assign({}, page, { name, beneath }, steps);
}

const failingPasses = fixture("standing-pass-failures");
failingPasses.factory_problems[0].what = "the product pass maintenance has failed 5 times in a row since 2026-10-04 12:00 UTC; latest: reconcile: last error";
failingPasses.factory_problems[0].whose = "the program manager's — the factory-flow program manager factory-flow-pm watches and must answer this finding; the development manager resolves the cause; the affected pass succeeding clears this finding";

const pages = [
  { name: "pass-failures", token: "t", standing: ok(failingPasses), throughput: ok(fixture("throughput-quiet")), spend: ok(fixture("spend-quiet")) },
  { name: "pass-failures-unreadable", token: "t", standing: ok(fixture("standing-pass-failures-unreadable")), throughput: ok(fixture("throughput-quiet")), spend: ok(fixture("spend-quiet")) },
  { name: "run-steps", token: "t", standing: ok(fixture("standing-run-steps")), throughput: ok(fixture("throughput-quiet")), spend: ok(fixture("spend-quiet")) },
  { name: "signin", token: "", standing: pending, throughput: pending, spend: pending },
  { name: "loading", token: "t", standing: pending, throughput: pending, spend: pending },
  { name: "quiet", token: "t", standing: ok(fixture("standing-quiet")), throughput: ok(fixture("throughput-quiet")), spend: ok(fixture("spend-quiet")) },
  { name: "busy", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")) },
  { name: "held", token: "t", standing: ok(fixture("standing-held")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")) },
  { name: "roles", token: "t", standing: ok(fixture("standing-roles")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")) },
  { name: "degraded", token: "t", standing: ok(fixture("standing-degraded")), throughput: ok(fixture("throughput-degraded")), spend: ok(fixture("spend-busy")) },
  { name: "unreadable", token: "t", standing: ok(fixture("standing-unreadable")), throughput: ok(fixture("throughput-unreadable")), spend: ok(fixture("spend-unreadable")) },
  { name: "throughput-pending", token: "t", standing: ok(fixture("standing-busy")), throughput: pending, spend: ok(fixture("spend-busy")) },
  { name: "throughput-refused", token: "t", standing: ok(fixture("standing-busy")), throughput: refused(503, "the state root could not be resolved"), spend: ok(fixture("spend-busy")) },
  // The spend box while the month is still being priced, which is the one
  // reading slow enough to be seen loading over a page that is otherwise drawn.
  { name: "spend-pending", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), spend: pending },
  { name: "refused", token: "t", standing: refused(503, "the state root could not be resolved: open /Users/somebody/Library/Application Support/Yoyodyne/state: permission denied"), throughput: pending, spend: pending },
  { name: "unreachable", token: "t", standing: unreachable, throughput: unreachable, spend: unreachable },
  { name: "wrong-token", token: "t", standing: refused(401, "this dashboard requires the token it printed when it started, as a bearer token"), throughput: pending, spend: pending },
  // The second poll fails after a first that succeeded: the page keeps what it
  // had and says it is stale.
  { name: "stale", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")), then: { "/api/standing": unreachable } },
  { name: "throughput-stale", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")), then: { "/api/throughput": refused(503, "the state root could not be resolved") } },
  // The dashboard's answers carry the age of the snapshot they were served
  // from: a standing older than two intervals keeps the small stale marker,
  // and one whose latest build failed names the failure beside its age.
  { name: "snapshot", token: "t", standing: ok(aged(fixture("standing-busy"), {})), throughput: ok(aged(fixture("throughput-busy"), { age_seconds: 20, interval_seconds: 60 })), spend: ok(fixture("spend-busy")) },
  { name: "snapshot-old", token: "t", standing: ok(aged(fixture("standing-busy"), { age_seconds: 45, stale: true })), throughput: ok(aged(fixture("throughput-busy"), { age_seconds: 20, interval_seconds: 60 })), spend: ok(fixture("spend-busy")) },
  { name: "snapshot-failed", token: "t", standing: ok(aged(fixture("standing-busy"), { age_seconds: 34, failure: "bd list timed out after 30s" })), throughput: ok(aged(fixture("throughput-busy"), { age_seconds: 95, interval_seconds: 60, failure: "the state root could not be resolved" })), spend: ok(fixture("spend-busy")) },
  { name: "spend-stale", token: "t", standing: ok(fixture("standing-busy")), throughput: ok(fixture("throughput-busy")), spend: ok(fixture("spend-busy")), then: { "/api/spend": refused(503, "the state root could not be resolved") } }
];

const scenarios = pages.concat([
  over("attention-run-steps", "run-steps", { open: [{ grouping: "attention" }] }),
  over("attention-run-queued", "run-steps", { open: [{ grouping: "attention" }, { entry: "owed-step:run-queued" }] }),
  over("attention-run-cleanup", "run-steps", { open: [{ grouping: "attention" }, { entry: "owed-step:run-cleanup" }] }),
  over("attention-run-dropped-cleanup", "run-steps", { open: [{ grouping: "attention" }, { entry: "owed-step:run-dropped" }] }),
  over("attention-run-dropped", "run-steps", { open: [{ grouping: "attention" }, { entry: "publication:run-dropped" }] }),
  // The pop-ups, each over a page rendered above. A pop-up scenario is that
  // page's scenario with what a reader clicks after it is drawn, and its
  // render is the pop-ups alone. The card, opened from Running now: an item in
  // flight, read whole; one still being read; one the tracker holds nothing
  // under; one that could not be read.
  over("card", "busy", { items: items("yoyodyne-ifd.141.3"), open: [{ item: "yoyodyne-ifd.141.3" }] }),
  over("card-loading", "busy", { items: { "/api/items/yoyodyne-ifd.201": pending }, open: [{ item: "yoyodyne-ifd.201" }] }),
  over("card-missing", "busy", { items: { "/api/items/yoyodyne-ifd.212": refused(404, "no work item is recorded under that id") }, open: [{ item: "yoyodyne-ifd.212" }] }),
  over("card-refused", "busy", { items: { "/api/items/yoyodyne-ifd.230": refused(503, "the work item could not be read: bd show failed with status failed and exit code 1: failed to open database: embeddeddolt: openat LOCK: operation not permitted") }, open: [{ item: "yoyodyne-ifd.230" }] }),
  // The grouping pop-up: a stage listed by title with each item's refusal;
  // the week's landed runs; a stage with nothing in it; a stage whose source
  // could not be read; the landed stage still being priced; the card opened
  // from a pile's entry, over it — a stopped run with its change preserved —
  // and then both closed with Escape, twice.
  over("grouping", "busy", { open: [{ grouping: "held" }] }),
  over("grouping-landed", "degraded", { open: [{ grouping: "landed:week" }] }),
  over("grouping-empty", "held", { open: [{ grouping: "startable" }] }),
  over("grouping-error", "degraded", { open: [{ grouping: "admitted" }] }),
  over("grouping-loading", "throughput-pending", { open: [{ grouping: "landed:today" }] }),
  // The month behind the spend box: one line per local day, the days no
  // priced record reaches saying so, and the two lines that follow them — the spend
  // whose moment could not be read, and the records that could not be priced.
  over("spend-days", "busy", { open: [{ grouping: "spend:days" }] }),
  // And the same listing in its other three states: a month nothing was spent
  // in and a spend that could not be read — each brought to an open listing
  // by a poll, since a box in either state offers no label to open — and one
  // still being priced, opened from the band's Cost tile.
  over("spend-days-empty", "busy", { open: [{ grouping: "spend:days" }, { poll: { "/api/spend": ok(fixture("spend-quiet")) } }] }),
  over("spend-days-error", "busy", { open: [{ grouping: "spend:days" }, { poll: { "/api/spend": ok(fixture("spend-unreadable")) } }] }),
  over("spend-days-loading", "spend-pending", { open: [{ grouping: "spend:days" }] }),
  over("grouping-card", "busy", { items: items("yoyodyne-ifd.153"), open: [{ grouping: "pile:held-decision" }, { item: "yoyodyne-ifd.153" }] }),
  over("closed", "busy", { items: items("yoyodyne-ifd.153"), open: [{ grouping: "pile:held-decision" }, { item: "yoyodyne-ifd.153" }], escape: 2 }),
  // A poll redraws the page under an open grouping, so the button that opened
  // it is gone by the time Escape closes it, and focus goes to the button now
  // carrying its key.
  over("closed-after-poll", "busy", { open: [{ grouping: "held" }, { poll: true }], escape: 1 }),
  // The Needs-a-human list, opened from the band's tile: every entry of the
  // attention line by what it is, its kind, and whose move it is; the same
  // list once a poll finds everything settled; and the list over a standing
  // whose attention line could not be read.
  over("attention", "busy", { open: [{ grouping: "attention" }] }),
  over("attention-empty", "busy", { open: [{ grouping: "attention" }, { poll: { "/api/standing": ok(fixture("standing-settled")) } }] }),
  over("attention-error", "degraded", { open: [{ grouping: "attention" }] }),
  // An entry's card over the list: a proposed change with the change and its
  // reason in full; a run that owes a step; an item a conversation carries,
  // and the item's own card opened from it; a card whose entry was settled by
  // the time the page next asked; and one whose line could not be read then.
  over("attention-amendment", "busy", { open: [{ grouping: "attention" }, { entry: "amendment:amendment-3f9a1c2e8b7d4f6a9c1e2b3d4f5a6b7c" }] }),
  over("attention-owed-step", "busy", { open: [{ grouping: "attention" }, { entry: "owed-step:run-2b6f0d3e8a1c4f7b9e5d2a8c6f1b3e70" }] }),
  over("attention-carried-item", "busy", { open: [{ grouping: "attention" }, { entry: "conversation-carried-item:yoyodyne-ifd.188" }] }),
  over("attention-carried-item-card", "busy", { items: items("yoyodyne-ifd.188"), open: [{ grouping: "attention" }, { entry: "conversation-carried-item:yoyodyne-ifd.188" }, { item: "yoyodyne-ifd.188" }] }),
  over("attention-settled", "busy", { open: [{ grouping: "attention" }, { entry: "owed-step:run-2b6f0d3e8a1c4f7b9e5d2a8c6f1b3e70" }, { poll: { "/api/standing": ok(fixture("standing-settled")) } }] }),
  over("attention-unreadable", "busy", { open: [{ grouping: "attention" }, { entry: "owed-step:run-2b6f0d3e8a1c4f7b9e5d2a8c6f1b3e70" }, { poll: { "/api/standing": ok(fixture("standing-degraded")) } }] }),
  // Escape twice from the item card opened off an entry card: the card
  // closes, then the list, and focus is back on the tile's label that opened
  // the list.
  over("attention-closed", "busy", { items: items("yoyodyne-ifd.188"), open: [{ grouping: "attention" }, { entry: "conversation-carried-item:yoyodyne-ifd.188" }, { item: "yoyodyne-ifd.188" }], escape: 2 }),
  // A program manager's current lane report, opened from its row: a stale
  // instance that is blocked as well, with an open restart request; a blocked
  // one whose report names a blocker the record does not bear out; a working
  // one that has written no report yet; one still being read; one the read
  // model no longer knows; one whose state could not be read; and one closed
  // with Escape, focus going back to the row's opener.
  over("report", "busy", { reports: reports("factory-pgm"), open: [{ report: "factory-pgm" }] }),
  over("report-blocked", "busy", { reports: reports("writing-pgm"), open: [{ report: "writing-pgm" }] }),
  over("report-unwritten", "busy", { reports: reports("docs-pgm"), open: [{ report: "docs-pgm" }] }),
  over("report-loading", "busy", { reports: { "/api/program-managers/factory-pgm": pending }, open: [{ report: "factory-pgm" }] }),
  over("report-missing", "busy", { reports: { "/api/program-managers/docs-pgm": refused(404, "no program manager instance is recorded under that name") }, open: [{ report: "docs-pgm" }] }),
  over("report-refused", "busy", { reports: { "/api/program-managers/writing-pgm": refused(503, "the state root could not be resolved: open /Users/somebody/Library/Application Support/Yoyodyne/state: permission denied") }, open: [{ report: "writing-pgm" }] }),
  over("report-closed", "busy", { reports: reports("factory-pgm"), open: [{ report: "factory-pgm" }], escape: 1 })
]);

function settle() {
  return new Promise((resolve) => setImmediate(() => setImmediate(resolve)));
}

async function run(scenario) {
  const shell = fs.readFileSync(path.join(assets, "shell.html"), "utf8").replace(/\{\{\.Product\}\}/g, "yoyodyne");
  const document = parse(shell);
  const script = fs.readFileSync(path.join(assets, "dashboard.js"), "utf8");

  const storage = new Map();
  if (scenario.token) {
    storage.set("yoyo-dashboard-token", scenario.token);
  }
  // The page asks again on timeouts it sets after each answer; a poll is every
  // pending one firing at once, which is the page asking again in between.
  const timeouts = new Map();
  let lastTimeout = 0;
  const poll = () => {
    const due = Array.from(timeouts.values());
    timeouts.clear();
    due.forEach((callback) => callback());
  };
  let answers = Object.assign({ "/api/standing": scenario.standing, "/api/throughput": scenario.throughput, "/api/spend": scenario.spend }, scenario.items || {}, scenario.reports || {});
  const requests = [];

  const fetch = (url, options) => {
    requests.push({ url, authorization: options && options.headers && options.headers.Authorization });
    const answer = answers[url];
    if (!answer) {
      return Promise.reject(new Error("no fixture answers " + url));
    }
    if (answer.pending) {
      return new Promise(() => {});
    }
    if (answer.unreachable) {
      return Promise.reject(new TypeError("Failed to fetch"));
    }
    return Promise.resolve({
      status: answer.status,
      ok: answer.ok,
      statusText: answer.statusText || "OK",
      json: () => Promise.resolve(JSON.parse(JSON.stringify(answer.body)))
    });
  };

  const window = {
    sessionStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, String(value)),
      removeItem: (key) => storage.delete(key)
    },
    setTimeout: (callback) => { lastTimeout += 1; timeouts.set(lastTimeout, callback); return lastTimeout; },
    clearTimeout: (id) => { timeouts.delete(id); }
  };
  const context = vm.createContext({ document, window, fetch, console });
  new vm.Script(script, { filename: "dashboard.js" }).runInContext(context);
  await settle();

  if (scenario.then) {
    answers = Object.assign({}, answers, scenario.then);
    poll();
    await settle();
  }

  // What a reader clicks: the one element carrying the key, found the way a
  // reader finds it — by what it opens, not where it is. An opener that is
  // not on the page is a failure of the page, and is said so.
  const openerFor = (attribute, value) => {
    const found = document.find((element) => element.tagName === "button" && element.getAttribute(attribute) === value);
    if (found.length === 0) {
      throw new Error(`${scenario.name}: nothing on the page opens ${attribute}=${value}`);
    }
    return found[0];
  };
  const opened = [];
  const clicked = [];
  for (const step of scenario.open || []) {
    if (step.poll) {
      // A poll given answers is the harness having moved in between: the
      // readings it names are answered differently from here on.
      if (typeof step.poll === "object") {
        answers = Object.assign({}, answers, step.poll);
      }
      poll();
      await settle();
      // The poll redraws the sections, so what was clicked is off the page:
      // that is the premise a scenario polling under a pop-up exists to test.
      if (clicked.some((element) => element.isConnected)) {
        throw new Error(`${scenario.name}: a poll left the clicked opener on the page`);
      }
      continue;
    }
    const opener = step.item ? ["data-item", step.item] : step.entry ? ["data-entry", step.entry] : step.report ? ["data-report", step.report] : ["data-grouping", step.grouping];
    opened.push(opener);
    clicked.push(openerFor(...opener));
    clicked[clicked.length - 1].dispatch("click", {});
    await settle();
  }
  for (let presses = scenario.escape || 0; presses > 0; presses -= 1) {
    document.dispatch("keydown", { key: "Escape" });
    await settle();
  }
  // A pop-up closed with Escape gives focus back to what opened it: the
  // button carrying the first key clicked, which after a poll is a button
  // drawn since rather than the one that was clicked.
  if (scenario.escape && opened.length > 0 && !openerFor(...opened[0]).focused) {
    throw new Error(`${scenario.name}: focus did not return to the opener after Escape`);
  }

  const page = document.getElementById("page");
  const sections = ["band", "spend", "live", "pipeline", "throughput", "capacity", "factory", "managers"];
  const popups = ["grouping", "card", "report"];
  const matrix = { page: page.getAttribute("data-state"), sections: {}, popups: {} };
  sections.forEach((id) => {
    matrix.sections[id] = document.getElementById(id).getAttribute("data-state");
  });
  // A pop-up that is hidden is closed, whatever state it was last drawn in.
  popups.forEach((id) => {
    const popup = document.getElementById(id);
    matrix.popups[id] = popup.getAttribute("hidden") !== null ? "closed" : popup.getAttribute("data-state");
  });
  // No token ever left the page except as a bearer to this origin.
  requests.forEach((request) => {
    if (!request.url.startsWith("/api/") || (scenario.token && request.authorization !== "Bearer " + scenario.token)) {
      throw new Error(`${scenario.name}: a request went to ${request.url} with ${request.authorization}`);
    }
  });
  // Nothing set an inline style or wrote markup: every element's attributes
  // are the shell's or a class, data-state, hidden, datetime, an id, or the
  // key of what an opener opens.
  document.find(() => true).forEach((element) => {
    for (const name of element.attributes.keys()) {
      if (name === "style" || name.startsWith("on")) {
        throw new Error(`${scenario.name}: <${element.tagName}> carries ${name}, which the policy refuses`);
      }
    }
  });

  // What is written is what a viewer sees: the page's states and each panel's
  // states are all in the document, and the stylesheet shows exactly one of
  // each, so the render keeps the one that is shown and drops the rest. A
  // render that carried every hidden state would say the same thing at three
  // times the length, and be read by nobody.
  const classes = (element) => element.className.split(/\s+/).filter(Boolean);
  const prune = (parent, shown, prefix) => {
    parent.childNodes.slice().forEach((child) => {
      if (!(child instanceof Element)) {
        return;
      }
      const states = classes(child).filter((name) => name.startsWith(prefix));
      if (states.length > 0 && !states.includes(prefix + shown)) {
        parent.removeChild(child);
      }
    });
  };
  sections.forEach((id) => {
    prune(document.getElementById(id), matrix.sections[id], "section-");
  });
  // A closed pop-up is dropped from the render, and an open one keeps the one
  // state it shows, which is under its card rather than at its root.
  popups.forEach((id) => {
    const popup = document.getElementById(id);
    if (matrix.popups[id] === "closed") {
      popup.parentNode.removeChild(popup);
      return;
    }
    popup.childNodes.forEach((child) => {
      if (child instanceof Element && classes(child).includes("popup-card")) {
        prune(child, matrix.popups[id], "section-");
      }
    });
  });
  prune(page, matrix.page, "state-");

  // A scenario that opened a pop-up renders the pop-ups it left open and
  // nothing else — the page beneath them is `beneath`'s render, and the
  // sentence that replaces it says so. The head stays, so the render opens
  // beside the real stylesheet exactly as the others do.
  if (scenario.beneath) {
    const body = document.find((element) => element.tagName === "body")[0];
    const kept = popups.map((id) => document.getElementById(id)).filter(Boolean);
    body.childNodes.slice().forEach((child) => body.removeChild(child));
    const note = document.createElement("p");
    note.className = "render-note";
    note.textContent = kept.length > 0
      ? `Opened over the ${scenario.beneath} render, which is not repeated here.`
      : `Everything opened was closed again; what is left is the ${scenario.beneath} render, which is not repeated here.`;
    body.appendChild(note);
    kept.forEach((popup) => body.appendChild(popup));
  }

  const html = document.root.childNodes
    .map((child) => (child instanceof Text ? child.data.trim() : child.serialize("")))
    .filter((line) => line !== "")
    .join("\n")
    .replace('href="/assets/dashboard.css"', 'href="../../assets/dashboard.css"')
    .replace('src="/assets/dashboard.js"', 'src="about:blank" data-note="the script ran once to produce this render; it is not loaded again here"');
  return { matrix, html: "<!doctype html>\n" + html + "\n", standingWarningAgeSeconds: context.standingWarningAgeSeconds };
}

// Exercise the standing's warning boundary through the page itself, reading
// its threshold rather than keeping a second value in the tests. These checks
// also run while updating the goldens, so recording a regression cannot pass.
async function checkStandingWarning(scenario, threshold) {
  for (const offset of [-1, 0, 1]) {
    const reading = aged(fixture("standing-busy"), { age_seconds: threshold + offset, stale: true });
    const rendered = await run(Object.assign({}, scenario, { standing: ok(reading) }));
    const warningHidden = rendered.html.includes('<p id="stale" class="stale" role="status" hidden>');
    assert.strictEqual(warningHidden, offset < 0, `standing at warning threshold ${offset < 0 ? "minus" : "plus"} ${Math.abs(offset)}s`);
    assert(rendered.html.includes('class="freshness freshness-stale">stale<'), "the small stale marker must remain at every warning boundary");
    assert(rendered.html.includes('<time id="observed-at" datetime="' + reading.observed_at + '">'), "the observed time must remain at every warning boundary");
    assert.strictEqual(rendered.html.includes("The reading of the standing is"), offset >= 0, "the standing's age warning must follow its threshold");
  }
}

async function main() {
  const check = process.argv.includes("--check");
  const at = process.argv.indexOf("--out");
  if (!check && (at === -1 || !process.argv[at + 1])) {
    console.error("usage: node render.js --out <directory> | --check");
    process.exit(2);
  }
  const out = check ? path.join(here, "renders") : process.argv[at + 1];
  if (!check) fs.mkdirSync(out, { recursive: true });
  for (const description of ["high, from the agent", "high, from the Codex configuration", "not reported, from the Codex configuration"]) {
    const reading = fixture("standing-busy");
    reading.running[0].backend = "codex";
    reading.running[0].model = "gpt-6.1-sol";
    reading.running[0].effort_description = description;
    reading.running[0].effort = description.endsWith("from the agent") ? "high" : "";
    const rendered = await run(Object.assign({}, pages.find((page) => page.name === "busy"), { standing: ok(reading) }));
    assert(rendered.html.includes("codex · gpt-6.1-sol · effort " + description), "the run card must project its effort source");
  }
  const matrix = {};
  const runSteps = fixture("standing-run-steps");
  for (const scenario of scenarios) {
    const rendered = await run(scenario);
    if (scenario.name === "snapshot-old") {
      await checkStandingWarning(scenario, rendered.standingWarningAgeSeconds);
    }
    // Exercise the four run histories through the actual page script, even
    // when this driver is called directly rather than through the Go suite.
    if (scenario.name === "run-steps") {
      assert(rendered.html.includes("Checks still running"));
      assert(rendered.html.includes('<span class="phase">checking</span>'));
    }
    if (scenario.name.startsWith("attention-run-")) {
      assert(!rendered.html.includes('data-entry="owed-step:run-live"'));
      for (const entry of runSteps.needs_human) {
        const cleanup = entry.kind === "owed-step" && !entry.owed_step.pull_request?.merge_queued;
        const cardScenario = "attention-" + entry.id + (entry.id === "run-dropped" && cleanup ? "-cleanup" : "");
        if (scenario.name !== "attention-run-steps" && scenario.name !== cardScenario) continue;
        assert.strictEqual(entry.label, cleanup ? "run not finished" : "merge stuck");
        assert.strictEqual(entry.mover, entry.kind === "publication" ? "development-manager" : "harness");
        for (const words of [entry.what, entry.whose, entry.label]) {
          assert(rendered.html.includes(escapeText(words)), `${scenario.name} must carry ${words}`);
        }
        if (scenario.name === "attention-run-steps") {
          assert(rendered.html.includes(`<span class="item-id">${entry.label}</span>`));
        } else {
          assert(rendered.html.replace(/\s+/g, " ").includes(`<dt>Kind</dt> <dd>${entry.label}</dd>`));
          assert(rendered.html.includes(`<h2 id="card-heading" class="popup-title">${entry.label}</h2>`));
        }
      }
    }
    const file = path.join(out, scenario.name + ".html");
    if (check) {
      assert.strictEqual(rendered.html, fs.readFileSync(file, "utf8"), scenario.name + " render differs");
    } else {
      fs.writeFileSync(file, rendered.html);
    }
    matrix[scenario.name] = rendered.matrix;
  }
  if (!check) fs.writeFileSync(path.join(out, "matrix.json"), JSON.stringify(matrix, null, 2) + "\n");
}

main().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
