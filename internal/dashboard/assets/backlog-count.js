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
// Windows are hours, calendar days, or weeks starting Monday, in the zone the
// caller names — the page names the reader's own, which is the operator's
// local zone the dashboard design asks for. Each hour starts where the zone's
// clock reads a whole hour, so an hour that is repeated when summer time ends
// is drawn twice, each named by its zone abbreviation. The hourly scale draws
// the last seven days, and the page says so.
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

  var hour = 3600000;
  var minute = 60000;
  var day = 86400000;
  var hourlyWindows = 7 * 24;

  // The split every item falls under when parts are counted under their
  // parent, and the words an origin nobody recorded is drawn under.
  var splitSeries = "split from an item";
  var unknownOrigin = "unknown: no origin recorded";

  // localZone is the zone this browser or process runs in, which is what the
  // page counts in.
  function localZone() {
    return new Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  }

  function parts(format, ms) {
    var found = {};
    format.formatToParts(new Date(ms)).forEach(function (part) { found[part.type] = part.value; });
    return found;
  }

  var dayParts = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", weekday: "short", day: "numeric", month: "short" });
  var yearLabel = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", year: "numeric" });

  // dayWords is a calendar day as "Mon 5 Oct", from the day-number instant.
  function dayWords(at) {
    var found = parts(dayParts, at.getTime());
    return found.weekday + " " + found.day + " " + found.month;
  }

  // calendar is the counting's view of one zone: which day, week, and hour an
  // instant falls in there, what a window is called, and what the zone is
  // called at an instant.
  function calendar(zone) {
    var civil = new Intl.DateTimeFormat("en-US", {
      timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23"
    });
    var named = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "short" });

    // dayNumber is the calendar day an instant falls on in the zone, as days
    // since 1 January 1970 on that calendar.
    function dayNumber(ms) {
      var found = parts(civil, ms);
      return Math.floor(Date.UTC(Number(found.year), Number(found.month) - 1, Number(found.day)) / day);
    }

    // hourStart is the instant the zone's clock last read a whole hour, at or
    // before ms: ms less the minutes the clock shows past the hour.
    function hourStart(ms) {
      var found = parts(civil, ms);
      var floored = Math.floor(ms / minute) * minute;
      return floored - Number(found.minute) * minute;
    }

    // windowOf is the key of the window an instant falls in: the instant its
    // hour starts, the zone's day number, or the day number of that week's
    // Monday.
    function windowOf(ms, scale) {
      if (scale === "hourly") {
        return hourStart(ms);
      }
      var number = dayNumber(ms);
      if (scale === "weekly") {
        var weekday = new Date(number * day).getUTCDay();
        return number - ((weekday + 6) % 7);
      }
      return number;
    }

    // next is the key of the window after one.
    function next(key, scale) {
      if (scale === "hourly") {
        return hourStart(key + hour + 30 * minute);
      }
      return key + (scale === "weekly" ? 7 : 1);
    }

    // zoneName is what the zone is called at an instant, such as PDT, and the
    // zone's own name where the platform has no shorter one for it.
    function zoneName(ms) {
      var found = parts(named, ms);
      return found.timeZoneName || zone;
    }

    // label is a window in words: "Mon 5 Oct, 14:00 PDT", "Mon 5 Oct", or
    // "week of Mon 5 Oct"; a day or week in another year than now names it.
    function label(key, scale, nowYear) {
      if (scale === "hourly") {
        var found = parts(civil, key);
        return dayWords(new Date(dayNumber(key) * day)) + ", " + found.hour + ":" + found.minute + " " + zoneName(key);
      }
      var at = new Date(key * day);
      var words = dayWords(at);
      if (yearLabel.format(at) !== nowYear) {
        words += " " + yearLabel.format(at);
      }
      return scale === "weekly" ? "week of " + words : words;
    }

    return { windowOf: windowOf, next: next, label: label, zoneName: zoneName };
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

  // series counts the items for one choice of the page's controls, at now,
  // in the zone choice.zone names, or this browser's own where it names none.
  // It returns the windows drawn, their labels, one series per group with a
  // value per window, and what had to be left out.
  function series(items, choice, now) {
    var scale = choice.scale, show = choice.show;
    var zone = choice.zone || localZone();
    var cal = calendar(zone);
    var windowOf = cal.windowOf;
    var result = {
      keys: [], labels: [], series: [], zone: cal.zoneName(now), zoneId: zone,
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
    var drawnFrom = scale === "hourly" ? Math.max(first, windowOf(now - (hourlyWindows - 1) * hour, scale)) : first;
    var nowYear = yearLabel.format(new Date(now));
    var position = {};
    for (var key = drawnFrom; key <= last; key = cal.next(key, scale)) {
      position[key] = result.keys.length;
      result.keys.push(key);
      result.labels.push(cal.label(key, scale, nowYear));
    }

    // index is a window's position among the drawn ones, -1 before them, and
    // past the end for one after now.
    var index = function (key) {
      if (key < drawnFrom) {
        return -1;
      }
      return Object.prototype.hasOwnProperty.call(position, key) ? position[key] : result.keys.length;
    };
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

  return { series: series, localZone: localZone, calendar: calendar, splitSeries: splitSeries, unknownOrigin: unknownOrigin, hourlyWindows: hourlyWindows };
})();

if (typeof module !== "undefined" && module.exports) {
  module.exports = backlogCount;
}
