#!/usr/bin/env node
// Runs the backlog page's script (assets/backlog.js) against the Chart.js the
// dashboard serves (assets/chart.umd.js), so the chart the page asks for is one
// that library draws: the script reads a backlog, the library is handed the
// page's canvas, and every series is drawn with its own colour, line pattern,
// and name, in the stylesheet's ink. Changing a control draws the chart again
// in place of the old one, and a dark stylesheet reaches the drawing. The
// chart of work closed against a week earlier is drawn on its own canvas, at a
// fixed time, with its bars green for more and red for fewer, the period under
// way dashed and faded, and its hover naming the count, what it was compared
// with, and the difference.
//
// No browser runs here. The page's elements are stand-ins that hold what the
// script writes, and the canvas is one whose drawing calls are recorded, which
// is what the assertions read. The library runs as it does outside a browser,
// so it sizes the chart from the canvas rather than from its box.
// TestTheBacklogChartDrawsWithTheServedLibraryUnderNode runs it.
//
// Usage: node backlog-chart.test.js

"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const assets = path.join(__dirname, "..", "assets");
const Chart = require(path.join(assets, "chart.umd.js"));
const backlogCount = require(path.join(assets, "backlog-count.js"));
const script = fs.readFileSync(path.join(assets, "backlog.js"), "utf8");

assert.ok(typeof Chart === "function" && /^4\./.test(Chart.version), "chart.umd.js is not Chart.js 4: " + Chart.version);

// A canvas whose 2D context records every call and every value set on it.
function recordingCanvas() {
  const calls = [];
  const state = { font: "12px sans-serif", lineWidth: 1, fillStyle: "#000", strokeStyle: "#000", globalAlpha: 1, textAlign: "start", textBaseline: "alphabetic", lineCap: "butt", lineJoin: "miter", lineDashOffset: 0 };
  const saved = [];
  let dash = [];
  const canvas = { width: 800, height: 400, calls };
  const methods = {
    measureText: (text) => ({ width: String(text).length * 7, actualBoundingBoxAscent: 9, actualBoundingBoxDescent: 3 }),
    save: () => saved.push(Object.assign({}, state, { dash })),
    restore: () => { const back = saved.pop(); if (back) { dash = back.dash; delete back.dash; Object.assign(state, back); } },
    setLineDash: (value) => { dash = value.slice(); calls.push({ name: "setLineDash", args: [value.slice()], state: Object.assign({}, state) }); },
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
        return (...args) => { const answer = methods[name](...args); if (name !== "setLineDash") calls.push({ name, args, state: Object.assign({}, state) }); return answer; };
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
function element(id) {
  const attributes = {};
  const listeners = {};
  const node = {
    id, children: [], textContent: "", value: "", checked: false,
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

// page is the backlog page's elements and window. Its local storage is the
// browser's for this dashboard's origin, holding by default the token the main
// page kept there under the key every page of the dashboard shares.
function page(tones, storage) {
  const kept = storage || new Map([["yoyo-dashboard-token", "token"]]);
  const elements = {};
  const byId = (id) => (elements[id] = elements[id] || element(id));
  byId("backlog-show").value = "filed";
  byId("backlog-split").value = "priority";
  byId("backlog-scale").value = "daily";
  byId("closed-scale").value = "daily";
  byId("closed-label").value = "";
  const canvases = { "backlog-canvas": recordingCanvas(), "closed-canvas": recordingCanvas() };
  const canvas = canvases["backlog-canvas"];
  const made = [];
  const closedMade = [];
  const closedConfigs = [];
  const window = {
    localStorage: {
      getItem: (key) => (kept.has(key) ? kept.get(key) : null),
      setItem: (key, value) => kept.set(key, String(value)),
      removeItem: (key) => kept.delete(key)
    },
    setTimeout: () => 0,
    clearTimeout() {},
    matchMedia: () => ({ matches: false, addEventListener() {} }),
    getComputedStyle: () => ({ getPropertyValue: (name) => tones[name] || "" }),
    // The page's script is handed the real library, with the canvas it names
    // swapped for the recording one; every chart it makes is kept. The
    // configuration is copied into the library's own context first: the
    // library fills in its defaults only on objects made by its own Object,
    // which in a browser every object is, and a configuration made in the
    // script's context here is not. Functions, the closed chart's hover, are
    // passed through as they are.
    Chart: function (target, config) {
      assert.ok(target === byId("backlog-canvas") || target === byId("closed-canvas"), "the chart is not drawn on one of the page's canvases");
      const chart = new Chart(canvases[target.id], inThisContext(config));
      if (target.id === "closed-canvas") {
        closedMade.push(chart);
        closedConfigs.push(config);
      } else {
        made.push(chart);
      }
      return chart;
    }
  };
  const document = {
    getElementById: byId,
    createElement: (tag) => element(tag),
    documentElement: element("html")
  };
  return { elements, byId, canvas, canvases, made, closedMade, closedConfigs, window, document, asked: [] };
}

// inThisContext copies a configuration's objects and arrays into this
// context, leaving its functions and other values as they are.
function inThisContext(value) {
  if (Array.isArray(value)) {
    return Array.from(value, (each) => inThisContext(each));
  }
  if (value && typeof value === "object") {
    const copy = {};
    Object.keys(value).forEach((key) => { copy[key] = inThisContext(value[key]); });
    return copy;
  }
  return value;
}

const day = 86400000;
const now = Date.now();
const iso = (at) => new Date(at).toISOString();
const items = [];
for (let index = 0; index < 60; index++) {
  const filed = now - (index % 20) * day - 3600000;
  items.push({
    id: "ex-" + index, title: "t", status: index % 3 === 0 ? "closed" : "open",
    filed_at: iso(filed), closed_at: index % 3 === 0 ? iso(filed + day / 2) : null,
    priority: index % 4, labels: index % 2 ? ["dashboard"] : [], kind: "task",
    parent: index % 7 === 0 ? "ex-1" : "", done_by: "developer run", state: index % 2 ? "ready" : "waiting",
    origin: { recorded: index % 5 !== 0, filed_by: index % 5 !== 0 ? "the Lead Product Manager" : "not recorded" }
  });
}
const reading = { observed_at: iso(now), items };

// run opens the page on a reading, by default the one above at the real
// time in this machine's zone; the closed chart's run fixes both. A storage
// given is the browser's for the origin, and a status given is the
// dashboard's answer in place of the reading.
function run(tones, fixed, storage, status) {
  const built = page(tones, storage);
  const answer = fixed ? fixed.reading : reading;
  const context = vm.createContext({
    window: built.window, document: built.document, Math, Intl, String, Error, Promise, console,
    backlogCount: fixed ? Object.assign({}, backlogCount, { localZone: () => fixed.zone }) : backlogCount,
    Date: fixed ? class extends Date { static now() { return fixed.now; } } : Date,
    fetch: (address, options) => {
      assert.strictEqual(address, "/api/backlog");
      built.asked.push(options.headers.Authorization);
      if (status) {
        return Promise.resolve({ status, ok: false, statusText: "Unauthorized", json: () => Promise.resolve({ error: "this dashboard requires the token it printed when it started, as a bearer token" }) });
      }
      return Promise.resolve({ status: 200, ok: true, json: () => Promise.resolve(answer) });
    }
  });
  vm.runInContext(script, context, { filename: "backlog.js" });
  return built;
}

function settle() {
  return new Promise((resolve) => setImmediate(resolve));
}

// drawnText is every piece of text the library drew, with the colour it drew
// it in.
function drawnText(canvas) {
  return canvas.calls.filter((call) => call.name === "fillText").map((call) => ({ text: String(call.args[0]), colour: call.state.fillStyle }));
}

async function main() {
  const light = { "--ink-soft": "#3d444d", "--rule": "#d9dde3" };
  const built = run(light);
  await settle();
  await settle();

  assert.strictEqual(built.byId("backlog").getAttribute("data-state"), "ready", "the backlog panel is not ready");
  assert.strictEqual(built.made.length, 1, "the page made " + built.made.length + " charts");
  assert.ok(!built.byId("backlog-chart").hasAttribute("hidden"), "the chart is hidden although the library is loaded");
  assert.ok(built.byId("backlog-nochart").hasAttribute("hidden"), "the note for a build without the library shows although the library is loaded");

  const chart = built.made[0];
  const counted = backlogCount.series(items, { show: "filed", split: "priority", scale: "daily", splitsAsFilings: false, zone: backlogCount.localZone() }, now);
  assert.ok(counted.series.length > 1, "the fixture splits into one series only");
  assert.deepStrictEqual(chart.data.labels, counted.labels);
  assert.deepStrictEqual(chart.data.datasets.map((each) => each.label), counted.series.map((each) => each.name));
  chart.data.datasets.forEach((each, index) => assert.deepStrictEqual(each.data, counted.series[index].values, each.label));

  // Every series is drawn: a line in its own colour and pattern, its point
  // shape, and its name in the legend in the stylesheet's ink.
  const strokes = built.canvas.calls.filter((call) => call.name === "stroke").map((call) => call.state.strokeStyle);
  const dashes = built.canvas.calls.filter((call) => call.name === "setLineDash").map((call) => JSON.stringify(call.args[0]));
  chart.data.datasets.forEach((each) => {
    assert.ok(strokes.includes(each.borderColor), "no line was drawn in " + each.borderColor + " for " + each.label);
  });
  assert.ok(dashes.includes("[6,4]") && dashes.includes("[2,3]"), "the series' line patterns were not drawn: " + dashes.join(" "));
  assert.deepStrictEqual(chart.getDatasetMeta(1).data[0].options.pointStyle, "triangle");
  const text = drawnText(built.canvas);
  counted.series.forEach((each) => {
    assert.ok(text.some((drawn) => drawn.text === each.name && drawn.colour === light["--ink-soft"]), "the legend does not name " + each.name + " in the page's ink: " + JSON.stringify(text));
  });
  assert.ok(text.some((drawn) => drawn.text === "filed in the day" && drawn.colour === light["--ink-soft"]), "the count axis is not titled in the page's ink");
  assert.ok(text.some((drawn) => drawn.text === counted.labels[0]), "the first window's label was not drawn");
  const grid = built.canvas.calls.filter((call) => call.name === "stroke" && call.state.strokeStyle === light["--rule"]);
  assert.ok(grid.length > 0, "the grid is not drawn in the stylesheet's rule colour");

  // A control changed: the old chart is destroyed and the new one drawn.
  built.byId("backlog-split").value = "filed_by";
  built.byId("backlog-split").fire("change");
  assert.strictEqual(built.made.length, 2);
  assert.strictEqual(Chart.getChart(built.canvas), built.made[1], "the old chart is still the canvas's chart");
  assert.ok(built.made[1].data.datasets.some((each) => each.label === backlogCount.unknownOrigin), "an origin nobody recorded is not drawn as unknown");
  for (const scale of ["hourly", "weekly"]) {
    for (const show of ["open", "closed"]) {
      built.byId("backlog-scale").value = scale;
      built.byId("backlog-show").value = show;
      built.byId("backlog-show").fire("change");
    }
  }
  assert.strictEqual(built.made.length, 6);

  // The dark stylesheet's ink and rules reach the drawing.
  const dark = { "--ink-soft": "#c5cad1", "--rule": "#2f353d" };
  const darker = run(dark);
  await settle();
  await settle();
  const darkText = drawnText(darker.canvas);
  assert.ok(darkText.length > 0 && darkText.every((drawn) => drawn.colour === dark["--ink-soft"]), "text drawn in a colour other than the dark ink: " + JSON.stringify(darkText.filter((drawn) => drawn.colour !== dark["--ink-soft"])));
  assert.ok(darker.canvas.calls.some((call) => call.name === "stroke" && call.state.strokeStyle === dark["--rule"]), "the grid is not drawn in the dark rule colour");

  await closedChart();
  await switchingTokens();

  console.log("backlog chart: drawn with Chart.js " + Chart.version + ", " + counted.series.length + " series");
}

// switchingTokens presses the header's Use a different token on a page
// holding one: the stored token is cleared and the page asks for another, and
// the one entered is stored in its place and carried by the next reading.
async function switchingTokens() {
  const key = "yoyo-dashboard-token";
  const stored = new Map([[key, "first"]]);
  const built = run({}, null, stored);
  await settle();
  await settle();
  const button = built.byId("switch-token");
  assert.strictEqual(button.getAttribute("hidden"), null, "a page holding a token offers no way to use a different one");
  button.fire("click");
  assert.ok(!stored.has(key), "using a different token left the old one stored");
  assert.strictEqual(built.byId("page").getAttribute("data-state"), "signin", "using a different token did not ask for one");
  assert.ok(button.hasAttribute("hidden"), "the page asking for a token still offers a different one");
  assert.ok(built.byId("signin-note").textContent.indexOf("has been cleared") >= 0, "the page does not say the token was cleared");
  built.byId("token").value = "second";
  built.byId("signin").fire("submit");
  await settle();
  await settle();
  assert.strictEqual(stored.get(key), "second", "the token entered was not stored in place of the old one");
  assert.deepStrictEqual(built.asked, ["Bearer first", "Bearer second"], "the reading after the switch did not carry the new token");
  assert.strictEqual(built.byId("switch-token").getAttribute("hidden"), null, "the page signed in again offers no way to use a different token");
}

// closedAt is one item closed at a Los Angeles summer-time clock reading.
function closedAt(clock, labels) {
  return {
    id: "c-" + clock, title: "t", status: "closed", filed_at: "2026-10-01T12:00:00Z",
    closed_at: new Date(Date.parse(clock + "Z") + 7 * 3600000).toISOString(),
    priority: 2, labels: labels || [], kind: "task", parent: "", done_by: "developer run", state: "closed",
    origin: { recorded: false, filed_by: "not recorded" }
  };
}

// cells is a table row's text, its heading first.
function cells(row) {
  return row.children.map((cell) => cell.textContent);
}

async function closedChart() {
  // Wednesday 28 October 10:00 in Los Angeles; the counting test works the
  // same closings through by hand.
  const fixed = {
    zone: "America/Los_Angeles",
    now: Date.parse("2026-10-28T17:00:00Z"),
    reading: {
      observed_at: "2026-10-28T17:00:00Z",
      items: [
        "2026-10-19T10:00:00", "2026-10-19T11:00:00", "2026-10-20T12:00:00", "2026-10-21T09:00:00", "2026-10-21T15:00:00",
        "2026-10-21T16:00:00", "2026-10-22T08:00:00", "2026-10-27T09:10:00", "2026-10-27T09:20:00", "2026-10-28T08:00:00",
        "2026-10-28T09:59:00"
      ].map((clock) => closedAt(clock)).concat([closedAt("2026-10-27T09:00:00", ["dashboard"])])
    }
  };
  const light = { "--ink-soft": "#3d444d", "--rule": "#d9dde3" };
  const built = run(light, fixed);
  await settle();
  await settle();
  assert.strictEqual(built.byId("closed").getAttribute("data-state"), "ready", "the closed panel is not ready");
  assert.strictEqual(built.closedMade.length, 1, "the page made " + built.closedMade.length + " closed charts");
  assert.ok(!built.byId("closed-chart").hasAttribute("hidden"));

  const config = built.closedConfigs[0];
  const [line, partial, bars] = config.data.datasets;
  assert.deepStrictEqual(config.data.labels, ["Mon 19 Oct", "Tue 20 Oct", "Wed 21 Oct", "Thu 22 Oct", "Fri 23 Oct", "Sat 24 Oct", "Sun 25 Oct", "Mon 26 Oct", "Tue 27 Oct", "Wed 28 Oct"]);
  assert.deepStrictEqual(line.data, [2, 1, 3, 1, 0, 0, 0, 0, 3, null], "the finished days' line");
  assert.deepStrictEqual(partial.data, [null, null, null, null, null, null, null, null, 3, 2], "the line into the day under way");
  assert.deepStrictEqual(bars.data, [2, 1, 3, 1, 0, 0, 0, -2, 2, 1], "the change from the same weekday a week earlier");

  // Bars: green for more, red for fewer, faded for the day under way; the
  // line into today dashed and faded.
  const green = "#1a7f37", red = "#cf222e", fadedGreen = "rgba(26, 127, 55, 0.35)";
  assert.strictEqual(bars.backgroundColor[0], green);
  assert.strictEqual(bars.backgroundColor[7], red, "a fall is not red");
  assert.strictEqual(bars.backgroundColor[8], green, "a rise is not green");
  assert.strictEqual(bars.backgroundColor[9], fadedGreen, "the day under way is not faded");
  assert.strictEqual(JSON.stringify(partial.borderDash), "[6,4]");
  assert.notStrictEqual(partial.borderColor, line.borderColor);
  const canvas = built.canvases["closed-canvas"];
  const fills = canvas.calls.filter((call) => call.name === "fill" || call.name === "fillRect").map((call) => call.state.fillStyle);
  assert.ok(fills.includes(green) && fills.includes(red) && fills.includes(fadedGreen), "the bars were not drawn green, red, and faded: " + JSON.stringify([...new Set(fills)]));
  assert.ok(canvas.calls.some((call) => call.name === "setLineDash" && JSON.stringify(call.args[0]) === "[6,4]"), "the line into the day under way was not drawn dashed");
  assert.ok(canvas.calls.some((call) => call.name === "stroke" && call.state.strokeStyle === partial.borderColor), "the faded line was not drawn");

  // Hovering on a point: one entry per day, its count, its comparison, and
  // the difference.
  const tooltip = config.options.plugins.tooltip;
  assert.ok(tooltip.filter({ datasetIndex: 2 }) && !tooltip.filter({ datasetIndex: 0 }) && !tooltip.filter({ datasetIndex: 1 }));
  assert.deepStrictEqual(inThisContext(tooltip.callbacks.label({ dataIndex: 7 })), ["closed: 0", "the same weekday a week earlier: 2", "change: -2"]);
  assert.deepStrictEqual(inThisContext(tooltip.callbacks.label({ dataIndex: 9 })), ["closed: 2 so far", "the same weekday a week earlier, to the same time of day: 1", "change: +1"]);
  assert.strictEqual(tooltip.callbacks.title([{ dataIndex: 9 }]), "Wed 28 Oct, so far");

  // The table, newest first.
  const rows = built.byId("closed-table").children[1].children;
  assert.deepStrictEqual(cells(rows[0]), ["Wed 28 Oct, so far", "2", "1", "+1"]);
  assert.deepStrictEqual(cells(rows[2]), ["Mon 26 Oct", "0", "2", "-2"]);

  // Weekly: this week so far against last week to the same weekday and time.
  built.byId("closed-scale").value = "weekly";
  built.byId("closed-scale").fire("change");
  assert.strictEqual(built.closedMade.length, 2);
  assert.strictEqual(Chart.getChart(canvas), built.closedMade[1], "the old closed chart is still the canvas's chart");
  const weekly = built.closedConfigs[1];
  assert.deepStrictEqual(weekly.data.labels, ["week of Mon 19 Oct", "week of Mon 26 Oct"]);
  assert.deepStrictEqual(weekly.data.datasets[2].data, [7, 1]);
  assert.deepStrictEqual(inThisContext(weekly.options.plugins.tooltip.callbacks.label({ dataIndex: 1 })), ["closed: 5 so far", "the week before, from Monday to the same weekday and time: 4", "change: +1"]);

  // The label filter offers every label a closed item carries and narrows
  // the chart to it.
  const offered = built.byId("closed-label").children.map((option) => option.value);
  assert.deepStrictEqual(offered, ["", "dashboard"]);
  built.byId("closed-scale").value = "daily";
  built.byId("closed-label").value = "dashboard";
  built.byId("closed-label").fire("change");
  const filtered = built.closedConfigs[built.closedConfigs.length - 1];
  assert.deepStrictEqual(filtered.data.labels, ["Tue 27 Oct", "Wed 28 Oct"]);
  assert.deepStrictEqual(filtered.data.datasets[2].data, [1, 0]);
  assert.strictEqual(built.byId("closed-label").value, "dashboard");

  await tokenKeeping(light);
}

// The backlog page reads with the token the main page kept for this origin,
// without asking; a browser holding none asks; and a token the dashboard
// refuses is removed from storage, for every page of the dashboard, and asked
// for again rather than tried again.
async function tokenKeeping(tones) {
  const key = "yoyo-dashboard-token";
  const browser = new Map([[key, "kept on the main page"]]);
  const signedIn = run(tones, null, browser);
  await settle();
  await settle();
  assert.strictEqual(signedIn.byId("page").getAttribute("data-state"), "ready", "the backlog page asks for a token the main page kept");
  assert.deepStrictEqual(signedIn.asked, ["Bearer kept on the main page"]);

  const otherPort = run(tones, null, new Map());
  await settle();
  assert.strictEqual(otherPort.byId("page").getAttribute("data-state"), "signin", "a backlog page whose origin holds no token does not ask for one");
  assert.deepStrictEqual(otherPort.asked, []);

  const rotated = new Map([[key, "replaced since"]]);
  const refused = run(tones, null, rotated, 401);
  await settle();
  await settle();
  assert.strictEqual(refused.byId("page").getAttribute("data-state"), "signin", "a refused token does not send the backlog page back to asking for it");
  assert(!rotated.has(key), "a token refused on the backlog page is still kept");
  assert.deepStrictEqual(refused.asked, ["Bearer replaced since"], "a refused token was tried again");
}

main().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
