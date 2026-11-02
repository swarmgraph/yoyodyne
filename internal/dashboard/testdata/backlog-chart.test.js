#!/usr/bin/env node
// Runs the backlog page's script (assets/backlog.js) against the Chart.js the
// dashboard serves (assets/chart.umd.js), so the chart the page asks for is one
// that library draws: the script reads a backlog, the library is handed the
// page's canvas, and every series is drawn with its own colour, line pattern,
// and name, in the stylesheet's ink. Changing a control draws the chart again
// in place of the old one, and a dark stylesheet reaches the drawing.
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

function page(tones) {
  const elements = {};
  const byId = (id) => (elements[id] = elements[id] || element(id));
  byId("backlog-show").value = "filed";
  byId("backlog-split").value = "priority";
  byId("backlog-scale").value = "daily";
  const canvas = recordingCanvas();
  const made = [];
  const window = {
    sessionStorage: { getItem: () => "token", setItem() {}, removeItem() {} },
    setTimeout: () => 0,
    clearTimeout() {},
    matchMedia: () => ({ matches: false, addEventListener() {} }),
    getComputedStyle: () => ({ getPropertyValue: (name) => tones[name] || "" }),
    // The page's script is handed the real library, with the canvas it names
    // swapped for the recording one; every chart it makes is kept. The
    // configuration is copied into the library's own context first: the
    // library fills in its defaults only on objects made by its own Object,
    // which in a browser every object is, and a configuration made in the
    // script's context here is not. A copy refuses a function, so a
    // configuration that came to carry one would fail here and need this
    // changed.
    Chart: function (target, config) {
      assert.strictEqual(target, byId("backlog-canvas"), "the chart is not drawn on the page's canvas");
      const chart = new Chart(canvas, structuredClone(config));
      made.push(chart);
      return chart;
    }
  };
  const document = {
    getElementById: byId,
    createElement: (tag) => element(tag),
    documentElement: element("html")
  };
  return { elements, byId, canvas, made, window, document };
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

function run(tones) {
  const built = page(tones);
  const context = vm.createContext({
    window: built.window, document: built.document, backlogCount, Date, Math, Intl, String, Error, Promise, console,
    fetch: (address) => {
      assert.strictEqual(address, "/api/backlog");
      return Promise.resolve({ status: 200, ok: true, json: () => Promise.resolve(reading) });
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

  console.log("backlog chart: drawn with Chart.js " + Chart.version + ", " + counted.series.length + " series");
}

main().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
