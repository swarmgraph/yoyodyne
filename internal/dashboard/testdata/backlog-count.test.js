#!/usr/bin/env node
// Holds the backlog page's counting (assets/backlog-count.js) to worked
// examples: days and Monday weeks in the zone the counting is given, whatever
// zone the machine is in, the machine's own zone where it is given none, an
// hour in a zone half an hour off the clock, the three things shown, parts split from an item counted under their parent,
// overlapping labels, an origin nobody recorded, times the tracker did not
// record, and the hour Los Angeles repeats when summer time ends.
// TestTheBacklogCountsHoldUnderNode runs it with the machine set to Tokyo
// time, so every case that names Los Angeles proves the counting follows the
// zone it is given rather than the machine's.
// It also compiles the page's own script, which a Go test cannot run.
//
// Usage: node backlog-count.test.js

"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const assets = path.join(__dirname, "..", "assets");
const count = require(path.join(assets, "backlog-count.js"));

// The page script has to parse, whatever else holds.
new vm.Script(fs.readFileSync(path.join(assets, "backlog.js"), "utf8"), { filename: "backlog.js" });

function item(fields) {
  return Object.assign({
    id: "x", title: "t", status: "open", filed_at: null, closed_at: null, priority: 2, labels: [],
    kind: "task", done_by: "developer run", state: "ready", origin: { recorded: false, filed_by: "not recorded" }
  }, fields);
}

const losAngeles = "America/Los_Angeles";

// inLosAngeles counts in Los Angeles time, which most examples below are
// worked in.
function inLosAngeles(items, choice, at) {
  return count.series(items, Object.assign({ zone: losAngeles }, choice), at);
}

function named(result, name) {
  const found = result.series.find((each) => each.name === name);
  assert.ok(found, "no series named " + name + " in " + JSON.stringify(result.series.map((each) => each.name)));
  return found.values;
}

const checks = [];
function check(name, run) {
  checks.push({ name, run });
}

// 06:30Z on 5 October is 23:30 on the 4th in Los Angeles; 08:00Z is 01:00 on
// the 5th. The 4th is a Sunday, so the two fall in different Monday weeks.
const lateSunday = "2026-10-05T06:30:00Z";
const earlyMonday = "2026-10-05T08:00:00Z";
const tuesday = "2026-10-06T18:00:00Z";
const now = Date.parse("2026-10-06T20:00:00Z");

const pair = [
  item({ id: "a", filed_at: lateSunday, closed_at: tuesday, status: "closed", state: "closed" }),
  item({ id: "b", filed_at: earlyMonday })
];

check("days are calendar days in the zone given", () => {
  const result = inLosAngeles(pair, { show: "filed", split: "none", scale: "daily" }, now);
  assert.deepStrictEqual(result.labels, ["Sun 4 Oct", "Mon 5 Oct", "Tue 6 Oct"]);
  assert.deepStrictEqual(named(result, "all work items"), [1, 1, 0]);
  assert.strictEqual(result.zone, "PDT");
});

check("weeks start on Monday", () => {
  const result = inLosAngeles(pair, { show: "filed", split: "none", scale: "weekly" }, now);
  assert.deepStrictEqual(result.labels, ["week of Mon 28 Sep", "week of Mon 5 Oct"]);
  assert.deepStrictEqual(named(result, "all work items"), [1, 1]);
});

check("open is what was filed and not yet closed at each window's end", () => {
  const result = inLosAngeles(pair, { show: "open", split: "none", scale: "daily" }, now);
  assert.deepStrictEqual(named(result, "all work items"), [1, 2, 1]);
});

check("closed counts closings in their window", () => {
  const result = inLosAngeles(pair, { show: "closed", split: "none", scale: "daily" }, now);
  assert.deepStrictEqual(named(result, "all work items"), [0, 0, 1]);
});

check("the hourly scale draws seven days and carries in what was open before", () => {
  const older = item({ id: "old", filed_at: "2026-09-20T12:00:00Z" });
  const result = inLosAngeles([older].concat(pair), { show: "open", split: "none", scale: "hourly" }, now);
  assert.strictEqual(result.keys.length, count.hourlyWindows);
  const values = named(result, "all work items");
  assert.strictEqual(values[0], 1);
  assert.strictEqual(values[values.length - 1], 2);
  assert.strictEqual(result.labels[result.labels.length - 1], "Tue 6 Oct, 13:00 PDT");
});

check("the hour Los Angeles repeats when summer time ends is drawn twice, by its zone", () => {
  const night = [item({ id: "n", filed_at: "2026-11-01T07:10:00Z" })];
  const result = inLosAngeles(night, { show: "filed", split: "none", scale: "hourly" }, Date.parse("2026-11-01T10:00:00Z"));
  assert.ok(result.labels.includes("Sun 1 Nov, 01:00 PDT"), result.labels.join(" | "));
  assert.ok(result.labels.includes("Sun 1 Nov, 01:00 PST"), result.labels.join(" | "));
});

check("the same items fall on other days in another zone", () => {
  // In Tokyo both filings are on Monday the 5th, and now is Wednesday the 7th.
  const result = count.series(pair, { show: "filed", split: "none", scale: "daily", zone: "Asia/Tokyo" }, now);
  assert.deepStrictEqual(result.labels, ["Mon 5 Oct", "Tue 6 Oct", "Wed 7 Oct"]);
  assert.deepStrictEqual(named(result, "all work items"), [2, 0, 0]);
  assert.strictEqual(result.zoneId, "Asia/Tokyo");
});

check("with no zone given the counting uses the machine's own", () => {
  const result = count.series(pair, { show: "filed", split: "none", scale: "daily" }, now);
  assert.strictEqual(result.zoneId, count.localZone());
  assert.strictEqual(result.zoneId, process.env.TZ || result.zoneId);
});

check("an hour starts where the zone's clock reads a whole hour", () => {
  // 08:00Z is 13:30 in Kolkata, which is half an hour off the clock in UTC.
  const result = count.series([item({ id: "k", filed_at: earlyMonday })], { show: "filed", split: "none", scale: "hourly", zone: "Asia/Kolkata" }, Date.parse("2026-10-05T10:00:00Z"));
  assert.ok(result.labels[0].indexOf("Mon 5 Oct, 13:00 ") === 0, result.labels[0]);
  assert.strictEqual(named(result, "all work items")[0], 1);
});

const family = [
  item({ id: "p", filed_at: earlyMonday, priority: 1 }),
  item({ id: "p.1", filed_at: earlyMonday, priority: 0, parent: "p" }),
  item({ id: "q", filed_at: earlyMonday, priority: 2 })
];

check("a part split from an item is counted under its parent by default", () => {
  const result = inLosAngeles(family, { show: "filed", split: "priority", scale: "daily" }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["P1", "P2", count.splitSeries]);
  assert.deepStrictEqual(named(result, count.splitSeries), [1, 0]);
});

check("the check box counts parts as filings, split like the rest", () => {
  const result = inLosAngeles(family, { show: "filed", split: "priority", scale: "daily", splitsAsFilings: true }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["P0", "P1", "P2"]);
});

check("an item with several labels counts under each, and one with none under no label", () => {
  const labelled = [
    item({ id: "l1", filed_at: earlyMonday, labels: ["reliability", "dashboard"] }),
    item({ id: "l2", filed_at: earlyMonday, labels: ["dashboard"] }),
    item({ id: "l3", filed_at: earlyMonday })
  ];
  const result = inLosAngeles(labelled, { show: "filed", split: "label", scale: "daily" }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["dashboard", "reliability", "no label"]);
  assert.deepStrictEqual(named(result, "dashboard"), [2, 0]);
  assert.strictEqual(result.overlapping, true);
});

check("an origin nobody recorded is drawn as unknown, never guessed", () => {
  const filed = [
    item({ id: "o1", filed_at: earlyMonday, origin: { recorded: true, filed_by: "operator" } }),
    item({ id: "o2", filed_at: earlyMonday })
  ];
  const result = inLosAngeles(filed, { show: "filed", split: "filed_by", scale: "daily" }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["operator", count.unknownOrigin]);
  assert.ok(count.unknownOrigin.indexOf("unknown") === 0);
});

check("ready or waiting and who does the work split by the model's own words", () => {
  const states = [
    item({ id: "s1", filed_at: earlyMonday, state: "ready", done_by: "developer run" }),
    item({ id: "s2", filed_at: earlyMonday, state: "waiting", done_by: "architect" }),
    item({ id: "s3", filed_at: earlyMonday, state: "waiting", done_by: "architect" })
  ];
  let result = inLosAngeles(states, { show: "filed", split: "state", scale: "daily" }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["waiting", "ready"]);
  result = inLosAngeles(states, { show: "filed", split: "done_by", scale: "daily" }, now);
  assert.deepStrictEqual(result.series.map((each) => each.name), ["architect", "developer run"]);
});

check("times the tracker did not record are left out and counted", () => {
  const gaps = [
    item({ id: "g1" }),
    item({ id: "g2", filed_at: earlyMonday, status: "closed", state: "closed" }),
    item({ id: "g3", filed_at: earlyMonday })
  ];
  const result = inLosAngeles(gaps, { show: "open", split: "none", scale: "daily" }, now);
  assert.strictEqual(result.noFiled, 1);
  assert.strictEqual(result.noClosed, 1);
  assert.deepStrictEqual(named(result, "all work items"), [1, 1]);
});

check("no items is no windows and no series", () => {
  const result = inLosAngeles([], { show: "filed", split: "none", scale: "daily" }, now);
  assert.deepStrictEqual(result.keys, []);
  assert.deepStrictEqual(result.series, []);
});

let failed = 0;
for (const each of checks) {
  try {
    each.run();
  } catch (error) {
    failed += 1;
    console.error("FAIL " + each.name + ": " + error.message);
  }
}
if (failed > 0) {
  process.exit(1);
}
console.log("ok " + checks.length + " checks");
