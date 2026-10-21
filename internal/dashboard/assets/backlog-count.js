// The backlog page's counting: from the items /api/backlog serves to the
// series the page draws. It touches no document, so the page's script draws
// what this returns and testdata/backlog-count.test.js runs it under Node.
//
// What is counted, per window:
//   - filed: items whose filing time falls in the window;
//   - closed: items whose closing time falls in the window;
//   - open: items filed by the window's end and not closed by it, counted as
//     every filing up to that window less every closing up to it, so a window
//     before the drawn range still counts what it left open.
// Windows are hours, Pacific calendar days, or Pacific weeks starting Monday.
// Hours are counted on the clock, since Pacific time moves by whole hours; the
// hourly scale draws the last seven days, and the page says so.
//
// A part split from an existing item is counted under its parent by default,
// as the dashboard design's backlog page rules: it is drawn as one series of
// its own, "split from an item", beside the rest, and never in them. The page's
// check box counts parts as filings instead, splitting them like any other
// item.
//
// Every split is by the item's value now — its priority, labels, who does it,
// whether it is ready — not the value it had in the window, because that is
// what the read model holds; the page says so. An item carrying several labels
// is counted under each, so those series overlap and the page says that too.
// An item whose filing or closing time the tracker did not record is left out
// of what that time decides and counted in the notes, never placed at a guess.

var backlogCount = (function () {
  "use strict";

  var zone = "America/Los_Angeles";
  var hour = 3600000;
  var day = 86400000;
  var hourlyWindows = 7 * 24;

  // The split every item falls under when parts are counted under their
  // parent, and the words an origin nobody recorded is drawn under.
  var splitSeries = "split from an item";
  var unknownOrigin = "unknown: no origin recorded";

  var civil = new Intl.DateTimeFormat("en-US", {
    timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit"
  });

  // dayNumber is the Pacific calendar day an instant falls on, as days since
  // 1 January 1970 on that calendar.
  function dayNumber(ms) {
    var parts = {};
    civil.formatToParts(new Date(ms)).forEach(function (part) { parts[part.type] = part.value; });
    return Math.floor(Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day)) / day);
  }

  // windowOf is the key of the window an instant falls in: an hour since the
  // epoch, a Pacific day number, or the day number of that week's Monday.
  function windowOf(ms, scale) {
    if (scale === "hourly") {
      return Math.floor(ms / hour);
    }
    var number = dayNumber(ms);
    if (scale === "weekly") {
      var weekday = new Date(number * day).getUTCDay();
      return number - ((weekday + 6) % 7);
    }
    return number;
  }

  function step(scale) {
    return scale === "weekly" ? 7 : 1;
  }

  var dayParts = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", weekday: "short", day: "numeric", month: "short" });

  // dayWords is a calendar day as "Mon 5 Oct", from the day-number instant.
  function dayWords(at) {
    var parts = {};
    dayParts.formatToParts(at).forEach(function (part) { parts[part.type] = part.value; });
    return parts.weekday + " " + parts.day + " " + parts.month;
  }
  var yearLabel = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", year: "numeric" });
  var hourOfDay = new Intl.DateTimeFormat("en-US", { timeZone: zone, hour: "2-digit", hourCycle: "h23" });

  // label is a window in words: "Mon 5 Oct, 14:00 PDT", "Mon 5 Oct", or
  // "week of Mon 5 Oct"; a day or week in another year than now names it.
  function label(key, scale, nowYear) {
    if (scale === "hourly") {
      var ms = key * hour;
      return dayWords(new Date(dayNumber(ms) * day)) + ", " + hourOfDay.format(new Date(ms)) + ":00 " + zoneName(ms);
    }
    var at = new Date(key * day);
    var words = dayWords(at);
    if (yearLabel.format(at) !== nowYear) {
      words += " " + yearLabel.format(at);
    }
    return scale === "weekly" ? "week of " + words : words;
  }

  // zoneName is what Pacific time is called at an instant: PDT or PST.
  function zoneName(ms) {
    var named = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "short" }).formatToParts(new Date(ms));
    for (var index = 0; index < named.length; index++) {
      if (named[index].type === "timeZoneName") {
        return named[index].value;
      }
    }
    return "Pacific time";
  }

  // groupsOf is every series one item counts in, for the chosen split.
  function groupsOf(item, split, splitsAsFilings) {
    if (item.parent && !splitsAsFilings) {
      return [splitSeries];
    }
    switch (split) {
      case "priority":
        return ["P" + item.priority];
      case "done_by":
        return [item.done_by || "not recorded"];
      case "label":
        return item.labels && item.labels.length ? item.labels.slice() : ["no label"];
      case "state":
        return [item.state || "not recorded"];
      case "filed_by":
        return [item.origin && item.origin.recorded && item.origin.filed_by ? item.origin.filed_by : unknownOrigin];
      default:
        return ["all work items"];
    }
  }

  function instant(text) {
    if (!text) {
      return null;
    }
    var ms = Date.parse(text);
    return isNaN(ms) ? null : ms;
  }

  // order puts priorities in their own order, then the larger series first,
  // and always last the series that are not a value of the split itself.
  function order(series, split) {
    var trailing = [splitSeries, unknownOrigin, "no label", "not recorded"];
    series.sort(function (left, right) {
      var leftTrails = trailing.indexOf(left.name), rightTrails = trailing.indexOf(right.name);
      if (leftTrails !== rightTrails) {
        return leftTrails - rightTrails;
      }
      if (split === "priority" && leftTrails < 0) {
        return Number(left.name.slice(1)) - Number(right.name.slice(1));
      }
      if (right.total !== left.total) {
        return right.total - left.total;
      }
      return left.name < right.name ? -1 : left.name > right.name ? 1 : 0;
    });
    return series;
  }

  // series counts the items for one choice of the page's controls, at now.
  // It returns the windows drawn, their labels, one series per group with a
  // value per window, and what had to be left out.
  function series(items, choice, now) {
    var scale = choice.scale, show = choice.show;
    var result = {
      keys: [], labels: [], series: [], zone: zoneName(now),
      noFiled: 0, noClosed: 0, overlapping: choice.split === "label", hourly: scale === "hourly"
    };
    var last = windowOf(now, scale);
    var first = null;
    var counted = [];
    items.forEach(function (item) {
      var filed = instant(item.filed_at);
      var closed = instant(item.closed_at);
      if (filed === null) {
        result.noFiled += 1;
        return;
      }
      if (item.state === "closed" && closed === null) {
        result.noClosed += 1;
      }
      var entry = {
        filed: windowOf(filed, scale),
        closed: closed === null ? null : windowOf(closed, scale),
        unplaced: item.state === "closed" && closed === null,
        groups: groupsOf(item, choice.split, choice.splitsAsFilings)
      };
      counted.push(entry);
      if (first === null || entry.filed < first) {
        first = entry.filed;
      }
    });
    if (first === null) {
      return result;
    }
    var drawnFrom = scale === "hourly" ? Math.max(first, last - hourlyWindows + 1) : first;
    var span = step(scale);
    var nowYear = yearLabel.format(new Date(now));
    for (var key = drawnFrom; key <= last; key += span) {
      result.keys.push(key);
      result.labels.push(label(key, scale, nowYear));
    }

    // index is a window's position among the drawn ones, or -1 before them.
    var index = function (key) { return key < drawnFrom ? -1 : Math.floor((key - drawnFrom) / span); };
    var byName = {};
    var groupNamed = function (name) {
      if (!byName[name]) {
        byName[name] = { name: name, values: result.keys.map(function () { return 0; }), carried: 0, total: 0 };
      }
      return byName[name];
    };
    counted.forEach(function (entry) {
      entry.groups.forEach(function (name) {
        var group = groupNamed(name);
        var at;
        if (show === "filed") {
          at = index(entry.filed);
          if (at >= 0 && at < group.values.length) {
            group.values[at] += 1;
            group.total += 1;
          }
        } else if (show === "closed") {
          if (entry.closed !== null) {
            at = index(entry.closed);
            if (at >= 0 && at < group.values.length) {
              group.values[at] += 1;
              group.total += 1;
            }
          }
        } else if (!entry.unplaced) {
          // Open: +1 from the window it was filed in, -1 from the one it
          // closed in; what happened before the drawn range is carried in.
          at = index(entry.filed);
          if (at < 0) {
            group.carried += 1;
          } else if (at < group.values.length) {
            group.values[at] += 1;
          }
          if (entry.closed !== null) {
            at = index(entry.closed);
            if (at < 0) {
              group.carried -= 1;
            } else if (at < group.values.length) {
              group.values[at] -= 1;
            }
          }
        }
      });
    });
    Object.keys(byName).forEach(function (name) {
      var group = byName[name];
      if (show === "open") {
        var running = group.carried;
        group.values = group.values.map(function (delta) { running += delta; return running; });
        group.total = group.values.reduce(function (most, value) { return Math.max(most, value); }, 0);
      }
      delete group.carried;
      // A series that is nothing in every window drawn is not drawn.
      if (group.values.some(function (value) { return value !== 0; })) {
        result.series.push(group);
      }
    });
    order(result.series, choice.split);
    return result;
  }

  return { series: series, zoneName: zoneName, windowOf: windowOf, splitSeries: splitSeries, unknownOrigin: unknownOrigin, hourlyWindows: hourlyWindows };
})();

if (typeof module !== "undefined" && module.exports) {
  module.exports = backlogCount;
}
