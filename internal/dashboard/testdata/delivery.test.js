#!/usr/bin/env node
// Holds the delivery page to a reading: its figures (assets/delivery-figures.js)
// and its script (assets/delivery.js) drawing them with the Chart.js the
// dashboard serves (assets/chart.umd.js), the one copy the backlog page draws
// with too.
//
// The reading is the file named on the command line, which
// TestTheDeliveryPageDrawsTheReadingUnderNode encodes from the read model's
// own types (deliveryFixture in delivery_test.go), so a field the page reads
// by a name the reading does not carry fails here. It runs with the machine
// in Tokyo time while the reading counts in Los Angeles time, so a period
// named from the browser's zone rather than the reading's would fail.
//
// What it holds: every measure the reading carries is drawn; a period whose
// records could not be read is "not recorded", in the table and in a note
// giving the reason, and a gap in the chart, never a zero; a period nothing
// merged in says so; a true zero is drawn as zero; the zone the periods are
// counted in is named, and the reader told where the browser's differs; and
// the charts are the served library's, drawn again in place of the old when
// the time scale changes, with the tables alone where the library is not
// served.
//
// Usage: node delivery.test.js <reading.json>

"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const assets = path.join(__dirname, "..", "assets");
const Chart = require(path.join(assets, "chart.umd.js"));
const figuresOf = require(path.join(assets, "delivery-figures.js"));
const script = fs.readFileSync(path.join(assets, "delivery.js"), "utf8");
const reading = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));

assert.ok(typeof Chart === "function" && /^4\./.test(Chart.version), "chart.umd.js is not Chart.js 4: " + Chart.version);

const notRecorded = figuresOf.notRecorded;
const nothingMerged = figuresOf.nothingMerged;

// ---- the figures ---------------------------------------------------------------

const daily = figuresOf.figures(reading, "daily");
const measure = (key) => {
  const found = daily.measures.find((each) => each.key === key);
  assert.ok(found, "no measure " + key);
  return found;
};
const values = (key, name) => {
  const found = measure(key).series.find((each) => each.name === name);
  assert.ok(found, key + " has no series " + name + ": " + measure(key).series.map((each) => each.name));
  return found.values;
};
// row is a measure's table row for one period, by its label.
const row = (key, label) => {
  const found = measure(key).rows.find((cells) => cells[0] === label);
  assert.ok(found, key + " has no row for " + label);
  return found.slice(1);
};
const noted = (key, words) => {
  assert.ok(measure(key).notes.some((line) => line.indexOf(words) >= 0), key + " has no note saying " + JSON.stringify(words) + ": " + JSON.stringify(measure(key).notes));
};

assert.deepStrictEqual(daily.measures.map((each) => each.key),
  ["merges", "lead_time", "stages", "change_failure", "recovery", "rework", "hand_steps", "first_pass", "tokens"]);
assert.strictEqual(daily.zone, "America/Los_Angeles");
const [unreadable, quiet, busy, today] = ["Fri 2 Oct", "Sat 3 Oct", "Sun 4 Oct", "Mon 5 Oct, so far"];
assert.deepStrictEqual(daily.labels, [unreadable, quiet, busy, today], "the days are not named as the reading dates them");
assert.deepStrictEqual(figuresOf.figures(reading, "weekly").labels, ["week of Mon 28 Sep, so far"]);
assert.strictEqual(figuresOf.periodWords({ label: "2025-12-29" }, "daily", "2026"), "Mon 29 Dec 2025");

// Merges: not recorded where the run records could not be read, and the
// reason said; zero where nothing merged, because that is a count.
assert.deepStrictEqual(values("merges", "changes merged"), [null, 0, 2, 1]);
assert.deepStrictEqual(row("merges", unreadable), [notRecorded]);
assert.deepStrictEqual(row("merges", quiet), ["0"]);
noted("merges", "Not recorded on 1 day (Fri 2 Oct): the run records could not be read: permission denied");
assert.deepStrictEqual(measure("merges").rows.map((cells) => cells[0]), [today, busy, quiet, unreadable], "the table is not newest first");
assert.deepStrictEqual(measure("merges").columns, ["day", "changes merged"]);
assert.deepStrictEqual(measure("merges").readFrom, ["the run records", "every run's event log"]);

// Lead time, whole and by stage, in hours on the chart and in words in the
// table; a stage the logs could not time is not recorded, with its reason.
assert.deepStrictEqual(values("lead_time", "median"), [null, null, 2, 1]);
assert.deepStrictEqual(values("lead_time", "90th percentile"), [null, null, 25, 1]);
assert.deepStrictEqual(row("lead_time", busy), ["2 h", "25 h", "2"]);
assert.deepStrictEqual(row("lead_time", quiet), [nothingMerged, nothingMerged, "0"]);
assert.deepStrictEqual(row("lead_time", unreadable), [notRecorded, notRecorded, notRecorded]);
assert.deepStrictEqual(measure("stages").series.map((each) => each.name),
  ["waiting for its first run", "developing", "running checks", "under review", "repairing", "merging"]);
assert.deepStrictEqual(values("stages", "under review"), [null, null, null, null]);
assert.deepStrictEqual(values("stages", "running checks"), [null, null, 0.5, 0.5]);
assert.strictEqual(row("stages", busy)[3], notRecorded);
assert.strictEqual(row("stages", busy)[2], "median 30 min, 90th percentile 1 h");
assert.strictEqual(row("stages", quiet)[0], nothingMerged);
noted("stages", "Not recorded on 2 days (Sun 4 Oct; Mon 5 Oct, so far): under review: no event log of a change merged records what its runs did");
noted("stages", "Review rounds per change: median 1, most 2");

// Change failure: a rate of zero over two merges is drawn as zero.
assert.deepStrictEqual(values("change_failure", "failed"), [null, null, 0, 100]);
assert.deepStrictEqual(row("change_failure", busy), ["0% (0)", "2"]);
assert.deepStrictEqual(row("change_failure", quiet), [nothingMerged, "0"]);
assert.deepStrictEqual(row("change_failure", unreadable), [notRecorded, notRecorded]);
noted("change_failure", "Failed: 1 reverted (yoyodyne-ifd.503)");
noted("change_failure", "1 change merged less than 48 hours ago");

// Recovery: not recorded, nothing cleared, and what is still broken.
assert.deepStrictEqual(values("recovery", "median"), [null, null, 1.5, null]);
assert.deepStrictEqual(row("recovery", unreadable), [notRecorded, notRecorded, notRecorded]);
assert.deepStrictEqual(row("recovery", quiet), ["nothing cleared", "nothing cleared", "0"]);
assert.deepStrictEqual(row("recovery", busy), ["1 h 30 min", "1 h 30 min", "2"]);
noted("recovery", "Not recorded on 1 day (Fri 2 Oct): the recurring passes could not be read");
noted("recovery", "Still broken: factory stall (the merge queue), for 1 h");

// Rework, with the share that records no kind beside it and the rate said
// to be a minimum.
assert.deepStrictEqual(values("rework", "bug fixes"), [null, null, 50, 0]);
assert.deepStrictEqual(values("rework", "no kind recorded"), [null, null, 50, 0]);
assert.deepStrictEqual(row("rework", busy), ["at least 50% (1)", "50% (1)", "2"]);
noted("rework", "The rate is a minimum");

// Hand steps: a floor, and a true zero where a change merged with none.
assert.deepStrictEqual(values("hand_steps", "recorded hand steps per change"), [null, null, 1.5, 0]);
assert.deepStrictEqual(row("hand_steps", busy), ["1.5", "3", "1"]);
assert.deepStrictEqual(row("hand_steps", quiet), [nothingMerged, "0", "0"]);
noted("hand_steps", "The count is ");

// First pass: no rate where no change's logs record both.
assert.deepStrictEqual(values("first_pass", "passed first time"), [null, null, 50, null]);
assert.deepStrictEqual(row("first_pass", today), ["no change's logs record both", "0"]);
noted("first_pass", "1 change's event logs do not record both");

// Tokens per change, by model, in thousands; today's usage records could not
// be read, which is not recorded rather than nothing spent.
assert.deepStrictEqual(measure("tokens").series.map((each) => each.name), ["claude-opus-5-5", "gpt-5-codex"]);
assert.deepStrictEqual(values("tokens", "claude-opus-5-5"), [null, null, 60, null]);
assert.deepStrictEqual(values("tokens", "gpt-5-codex"), [null, null, 15, null]);
assert.deepStrictEqual(row("tokens", busy), ["claude-opus-5-5: 60,000; gpt-5-codex: 15,000", "2", "0"]);
assert.deepStrictEqual(row("tokens", today), [notRecorded, notRecorded, notRecorded]);
noted("tokens", "Not recorded on 1 day (Mon 5 Oct, so far): the usage log could not be read: permission denied");

// Nowhere is a period the table calls not recorded drawn as a figure.
daily.measures.forEach((each) => {
  daily.labels.forEach((label, index) => {
    const cells = row(each.key, label);
    if (cells.every((cell) => cell === notRecorded)) {
      each.series.forEach((series) => assert.strictEqual(series.values[index], null, each.key + " draws " + series.name + " on " + label + ", which its table calls not recorded"));
    }
  });
});

// ---- the page ------------------------------------------------------------------

// A canvas whose 2D context records every call and every value set on it.
function recordingCanvas() {
  const calls = [];
  const state = { font: "12px sans-serif", lineWidth: 1, fillStyle: "#000", strokeStyle: "#000", globalAlpha: 1, textAlign: "start", textBaseline: "alphabetic", lineCap: "butt", lineJoin: "miter", lineDashOffset: 0 };
  const saved = [];
  let dash = [];
  const canvas = { width: 800, height: 300, calls };
  const methods = {
    measureText: (text) => ({ width: String(text).length * 7, actualBoundingBoxAscent: 9, actualBoundingBoxDescent: 3 }),
    save: () => saved.push(Object.assign({}, state, { dash })),
    restore: () => { const back = saved.pop(); if (back) { dash = back.dash; delete back.dash; Object.assign(state, back); } },
    setLineDash: (value) => { dash = value.slice(); },
    getLineDash: () => dash.slice(),
    createLinearGradient: () => ({ addColorStop() {} }),
    createRadialGradient: () => ({ addColorStop() {} }),
    createPattern: () => ({}),
    getImageData: () => ({ data: [] }),
    isPointInPath: () => false
  };
  const context = new Proxy({}, {
    get(target, name) {
      if (name === "canvas") return canvas;
      if (name in methods) {
        return (...args) => { const answer = methods[name](...args); calls.push({ name, args, state: Object.assign({}, state) }); return answer; };
      }
      if (name in state) return state[name];
      return (...args) => { calls.push({ name, args, state: Object.assign({}, state) }); };
    },
    set(target, name, value) { state[name] = value; return true; }
  });
  canvas.getContext = (kind) => (kind === "2d" ? context : null);
  return canvas;
}

// A stand-in for an element: attributes, text, children, and listeners.
function element(tag, id) {
  const attributes = {};
  const listeners = {};
  const node = {
    tag, id, children: [], textContent: "", value: "", checked: false,
    setAttribute(name, value) { attributes[name] = String(value); },
    removeAttribute(name) { delete attributes[name]; },
    getAttribute(name) { return name in attributes ? attributes[name] : null; },
    hasAttribute(name) { return name in attributes; },
    appendChild(child) { node.children.push(child); return child; },
    removeChild(child) { node.children.splice(node.children.indexOf(child), 1); return child; },
    get firstChild() { return node.children[0] || null; },
    addEventListener(kind, listener) { (listeners[kind] = listeners[kind] || []).push(listener); },
    fire(kind) { (listeners[kind] || []).forEach((listener) => listener({ preventDefault() {} })); },
    focus() {}
  };
  return node;
}

// all is every element under one, itself included.
function all(node) {
  return [node].concat(...node.children.map(all));
}

function words(node) {
  return all(node).map((each) => each.textContent).join(" | ");
}

function page(withLibrary) {
  const elements = {};
  const byId = (id) => (elements[id] = elements[id] || element("div", id));
  byId("delivery-scale").value = "daily";
  const made = [];
  const window = {
    localStorage: { getItem: () => "token", setItem() {}, removeItem() {} },
    setTimeout: () => 0,
    clearTimeout() {},
    matchMedia: () => ({ matches: false, addEventListener() {} }),
    getComputedStyle: () => ({ getPropertyValue: (name) => ({ "--ink-soft": "#3d444d", "--rule": "#d9dde3" })[name] || "" })
  };
  if (withLibrary) {
    // The real library, on a recording canvas for each chart, with the
    // configuration copied into the library's own context first, as
    // backlog-chart.test.js explains.
    window.Chart = function (target, config) {
      assert.strictEqual(target.tag, "canvas", "a chart is not drawn on a canvas");
      const canvas = recordingCanvas();
      const chart = new Chart(canvas, structuredClone(config));
      made.push({ target, chart, canvas });
      return chart;
    };
  }
  const document = {
    getElementById: byId,
    createElement: (tag) => element(tag),
    documentElement: element("html")
  };
  return { byId, made, window, document };
}

function run(withLibrary) {
  const built = page(withLibrary);
  const served = Object.assign({ snapshot: { taken_at: reading.observed_at, age_seconds: 5, interval_seconds: 60, build_seconds: 2, stale: false } }, reading);
  const context = vm.createContext({
    window: built.window, document: built.document, deliveryFigures: figuresOf, Date, Math, Intl, String, Error, Promise, console,
    fetch: (address, options) => {
      assert.strictEqual(address, "/api/delivery");
      assert.strictEqual(options.headers.Authorization, "Bearer token");
      return Promise.resolve({ status: 200, ok: true, json: () => Promise.resolve(served) });
    }
  });
  vm.runInContext(script, context, { filename: "delivery.js" });
  return built;
}

function settle() {
  return new Promise((resolve) => setImmediate(resolve));
}

async function main() {
  const built = run(true);
  await settle();
  await settle();

  assert.strictEqual(built.byId("page").getAttribute("data-state"), "ready", "the page is not ready");
  const zone = built.byId("delivery-zone").textContent;
  assert.ok(zone.indexOf("counted in America/Los_Angeles (PDT)") >= 0, "the zone is not named: " + zone);
  assert.ok(zone.indexOf("This browser is in Asia/Tokyo (GMT+9)") >= 0, "the reader is not told the browser's zone differs: " + zone);
  assert.ok(built.byId("delivery-nochart").hasAttribute("hidden"), "the note for a build without the library shows although it is loaded");
  assert.ok(words(built.byId("delivery-sources")).indexOf("Could not read the usage log: permission denied") >= 0, "an unread source is not named");

  const panels = built.byId("delivery-measures").children;
  assert.deepStrictEqual(panels.map((each) => each.getAttribute("id")), daily.measures.map((each) => "delivery-" + each.key));
  assert.strictEqual(built.made.length, daily.measures.length, "not every measure was charted");
  built.made.forEach(({ chart, canvas }, index) => {
    const expected = daily.measures[index];
    assert.deepStrictEqual(chart.data.labels, daily.labels);
    assert.deepStrictEqual(chart.data.datasets.map((each) => each.label), expected.series.map((each) => each.name));
    chart.data.datasets.forEach((each, at) => {
      assert.deepStrictEqual(each.data, expected.series[at].values, expected.key + ": " + each.label);
      assert.strictEqual(each.spanGaps, false, expected.key + " joins its line across a period with no figure");
    });
    // A period with no figure is not a point: the library skips it.
    const meta = chart.getDatasetMeta(0);
    expected.series[0].values.forEach((value, at) => {
      assert.strictEqual(meta.data[at].skip, value === null, expected.key + " on " + daily.labels[at]);
    });
    assert.ok(canvas.calls.some((call) => call.name === "fillText" && call.args[0] === expected.axis), expected.key + "'s axis is not titled");
  });

  // The tables carry the same words as the figures, and are closed under a
  // chart.
  const merges = panels[0];
  const table = all(merges).find((each) => each.tag === "table");
  assert.ok(words(table).indexOf(notRecorded) >= 0, "the merges table does not say not recorded");
  assert.ok(!all(merges).find((each) => each.tag === "details").hasAttribute("open"));
  assert.ok(words(merges).indexOf("Not recorded on 1 day (Fri 2 Oct)") >= 0, "the merges panel does not say why a day is not recorded");

  // The time scale drawn again: every old chart destroyed, new ones on weeks.
  const before = built.made.slice();
  built.byId("delivery-scale").value = "weekly";
  built.byId("delivery-scale").fire("change");
  assert.strictEqual(built.made.length, before.length * 2);
  before.forEach(({ canvas }) => assert.strictEqual(Chart.getChart(canvas), undefined, "an old chart was not destroyed"));
  assert.deepStrictEqual(built.made[before.length].chart.data.labels, ["week of Mon 28 Sep, so far"]);

  // Without the library: the tables alone, open, and the note saying why.
  const bare = run(false);
  await settle();
  await settle();
  assert.strictEqual(bare.made.length, 0);
  assert.ok(!bare.byId("delivery-nochart").hasAttribute("hidden"), "a build without the library does not say so");
  bare.byId("delivery-measures").children.forEach((panel) => {
    assert.ok(!all(panel).some((each) => each.tag === "canvas"), "a canvas is drawn without the library");
    assert.ok(all(panel).find((each) => each.tag === "details").hasAttribute("open"), "a table is closed without the library");
  });

  console.log("delivery page: " + daily.measures.length + " measures drawn with Chart.js " + Chart.version);
}

main().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
