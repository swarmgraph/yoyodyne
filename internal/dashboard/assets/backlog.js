// The backlog page's script: hold the token, read /api/backlog with it, and
// draw what backlog-count.js counts from it as a chart and as a table.
//
// The token is the main page's: the same session storage key on the same
// origin, so a tab signed in on the main page opens this one signed in. The
// same rules hold as there (dashboard.js): the token goes in the Authorization
// header of a request to this origin and nowhere else, every value goes into
// the page through textContent, and nothing here sets a style.
//
// The chart is drawn by Chart.js where the dashboard serves it (chartlib.go)
// and the table is always there under it, open by itself where the library is
// not. Each series is told apart by its point shape and its line pattern as
// well as its colour, and the colours are read from the stylesheet, so the
// chart follows the reader's light or dark setting.
//
// The backlog is read when the page opens and every five minutes after, since
// it lists the whole tracker; it is read once per page, not polled like the
// main page's readings.

(function () {
  "use strict";

  var storageKey = "yoyo-dashboard-token";
  var every = 5 * 60000;

  var page = document.getElementById("page");
  var signin = document.getElementById("signin");
  var signinNote = document.getElementById("signin-note");
  var tokenField = document.getElementById("token");
  var panel = document.getElementById("backlog");
  var controls = {
    show: document.getElementById("backlog-show"),
    split: document.getElementById("backlog-split"),
    scale: document.getElementById("backlog-scale"),
    splits: document.getElementById("backlog-splits")
  };

  var reading = null;
  var chart = null;
  var timer = null;
  var generation = 0;

  // Point shapes and line patterns, in step with the palette, so no two of
  // the first several series differ by colour alone.
  var shapes = ["circle", "triangle", "rect", "rectRot", "star", "crossRot", "cross", "rectRounded"];
  var dashes = [[], [6, 4], [2, 3], [10, 3, 2, 3]];
  var palette = ["#0072b2", "#e69f00", "#009e73", "#cc79a7", "#56b4e9", "#d55e00", "#999999", "#f0e442"];

  function text(id, words) {
    document.getElementById(id).textContent = words;
  }

  function setHidden(element, hidden) {
    if (hidden) {
      element.setAttribute("hidden", "");
    } else {
      element.removeAttribute("hidden");
    }
  }

  function show(state) {
    page.setAttribute("data-state", state);
  }

  function section(state, words) {
    panel.setAttribute("data-state", state);
    if (state === "empty") {
      text("backlog-empty", words);
    }
  }

  // ---- the token -------------------------------------------------------------

  function token() {
    try {
      return window.sessionStorage.getItem(storageKey) || "";
    } catch (error) {
      return "";
    }
  }

  function remember(value) {
    try {
      window.sessionStorage.setItem(storageKey, value);
    } catch (error) {
      // Storage refused: the token is held for this page load only.
    }
  }

  function forget() {
    try {
      window.sessionStorage.removeItem(storageKey);
    } catch (error) {
      // Nothing to forget where nothing could be kept.
    }
  }

  function askForToken(note) {
    generation += 1;
    window.clearTimeout(timer);
    signinNote.textContent = note || "";
    tokenField.value = "";
    show("signin");
    tokenField.focus();
  }

  // ---- reading ---------------------------------------------------------------

  function read(current) {
    var mine = generation;
    fetch("/api/backlog", {
      headers: { Accept: "application/json", Authorization: "Bearer " + current },
      cache: "no-store"
    })
      .then(function (response) {
        if (response.status === 401) {
          forget();
          askForToken("that is not the token this dashboard printed when it started");
          return null;
        }
        return response.json().then(function (body) {
          if (!response.ok) {
            throw new Error((body && body.error) || (response.status + " " + response.statusText));
          }
          return body;
        }, function () {
          throw new Error(response.status + " " + response.statusText + ", and the answer was not JSON");
        });
      })
      .then(function (body) {
        if (body && mine === generation) {
          reading = body;
          draw();
          schedule(current);
        }
      })
      .catch(function (error) {
        if (mine !== generation) {
          return;
        }
        failed(error.message.indexOf("Failed to fetch") >= 0 || error.name === "TypeError"
          ? "the dashboard could not be reached (" + error.message + "); the yoyo dashboard process may have stopped"
          : error.message);
        schedule(current);
      });
  }

  function schedule(current) {
    window.clearTimeout(timer);
    timer = window.setTimeout(function () { read(current); }, every);
  }

  // failed says why there is no backlog. Where an earlier reading is held it
  // stays drawn and the failure is said above it; otherwise the page is the
  // failure.
  function failed(reason) {
    if (reading) {
      var banner = document.getElementById("backlog-readiness");
      banner.textContent = "The latest reading failed, so this is the one before it: " + reason;
      setHidden(banner, false);
      return;
    }
    text("problem", reason);
    text("remedy", "This page asks again in five minutes; reloading it asks now.");
    show("error");
  }

  // ---- drawing ---------------------------------------------------------------

  function choice() {
    return {
      show: controls.show.value,
      split: controls.split.value,
      scale: controls.scale.value,
      splitsAsFilings: controls.splits.checked
    };
  }

  var showWords = { filed: "filed", open: "open", closed: "closed" };
  var scaleWords = { hourly: "hour", daily: "day", weekly: "week" };

  function draw() {
    show("ready");
    var observed = document.getElementById("observed-at");
    observed.textContent = new Date(reading.observed_at).toLocaleString("en-US", { timeZone: "America/Los_Angeles", timeZoneName: "short" });
    observed.setAttribute("datetime", reading.observed_at);

    var banner = document.getElementById("backlog-readiness");
    banner.textContent = reading.readiness_problem ? "Ready or waiting could not be read for every item: " + reading.readiness_problem : "";
    setHidden(banner, !reading.readiness_problem);

    if (reading.tracker_problem) {
      text("backlog-problem", reading.tracker_problem);
      text("backlog-remedy", "Nothing is charted, because a chart drawn from part of the tracker would be wrong. This page asks again in five minutes.");
      section("error");
      dropChart();
      return;
    }
    var items = reading.items || [];
    var now = Date.now();
    text("backlog-zone", "Every hour, day, and week is in Pacific time (" + backlogCount.zoneName(now) + "), and weeks start on Monday.");
    if (items.length === 0) {
      section("empty", "The tracker holds no work items yet, so there is nothing to chart.");
      dropChart();
      return;
    }
    var picked = choice();
    var counted = backlogCount.series(items, picked, now);
    if (counted.series.length === 0) {
      section("empty", "No work item was " + showWords[picked.show] + " in the windows this scale draws.");
      dropChart();
      return;
    }
    section("ready");
    text("backlog-window", windowWords(picked, counted));
    notes(picked, counted);
    table(picked, counted);
    drawChart(picked, counted);
  }

  function windowWords(picked, counted) {
    var what = picked.show === "open"
      ? "Work items open at the end of each " + scaleWords[picked.scale]
      : "Work items " + showWords[picked.show] + " in each " + scaleWords[picked.scale];
    var span = counted.hourly
      ? ", over the last seven days"
      : ", from " + counted.labels[0] + " to " + counted.labels[counted.labels.length - 1];
    return what + span + "; the last " + scaleWords[picked.scale] + " is still under way.";
  }

  function notes(picked, counted) {
    var list = document.getElementById("backlog-notes");
    while (list.firstChild) {
      list.removeChild(list.firstChild);
    }
    var lines = [];
    if (picked.split !== "none") {
      lines.push("Each item is split by what it is now — its priority, labels, who does it, and whether it is ready — not by what it was then.");
    }
    if (counted.overlapping) {
      lines.push("An item with several labels is counted under each of them, so these series add up to more than the items.");
    }
    if (!picked.splitsAsFilings) {
      lines.push("A part split from an item is counted under the item it came from, in the series “" + backlogCount.splitSeries + "”, not as new work.");
    }
    if (counted.noFiled > 0) {
      lines.push(counted.noFiled + " " + plural(counted.noFiled, "item has", "items have") + " no filing time recorded and " + plural(counted.noFiled, "is", "are") + " not counted.");
    }
    if (counted.noClosed > 0) {
      lines.push(counted.noClosed + " closed " + plural(counted.noClosed, "item has", "items have") + " no closing time recorded and " + plural(counted.noClosed, "is", "are") + " left out of closed and open.");
    }
    lines.forEach(function (line) {
      var entry = document.createElement("li");
      entry.textContent = line;
      list.appendChild(entry);
    });
  }

  function plural(count, one, many) {
    return count === 1 ? one : many;
  }

  // table is the same figures as the chart, newest window first.
  function table(picked, counted) {
    var element = document.getElementById("backlog-table");
    while (element.firstChild) {
      element.removeChild(element.firstChild);
    }
    var head = document.createElement("thead");
    var headRow = document.createElement("tr");
    [scaleWords[picked.scale]].concat(counted.series.map(function (each) { return each.name; })).forEach(function (name) {
      var cell = document.createElement("th");
      cell.setAttribute("scope", "col");
      cell.textContent = name;
      headRow.appendChild(cell);
    });
    head.appendChild(headRow);
    element.appendChild(head);
    var body = document.createElement("tbody");
    for (var index = counted.labels.length - 1; index >= 0; index--) {
      var row = document.createElement("tr");
      var when = document.createElement("th");
      when.setAttribute("scope", "row");
      when.textContent = counted.labels[index];
      row.appendChild(when);
      counted.series.forEach(function (each) {
        var cell = document.createElement("td");
        cell.textContent = String(each.values[index]);
        row.appendChild(cell);
      });
      body.appendChild(row);
    }
    element.appendChild(body);
  }

  function dropChart() {
    if (chart) {
      chart.destroy();
      chart = null;
    }
  }

  // tone is one of the stylesheet's colours, so the chart's text and rules
  // follow the reader's light or dark setting.
  function tone(name) {
    return window.getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function drawChart(picked, counted) {
    var library = window.Chart;
    var figures = document.getElementById("backlog-figures");
    setHidden(document.getElementById("backlog-chart"), !library);
    setHidden(document.getElementById("backlog-nochart"), !!library);
    if (!library) {
      figures.setAttribute("open", "");
      return;
    }
    dropChart();
    var still = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    var ink = tone("--ink-soft"), rule = tone("--rule");
    chart = new library(document.getElementById("backlog-canvas"), {
      type: "line",
      data: {
        labels: counted.labels,
        datasets: counted.series.map(function (each, index) {
          var colour = palette[index % palette.length];
          return {
            label: each.name,
            data: each.values,
            borderColor: colour,
            backgroundColor: colour,
            borderWidth: 2,
            borderDash: dashes[index % dashes.length],
            pointStyle: shapes[index % shapes.length],
            pointRadius: counted.labels.length > 60 ? 2 : 4,
            pointHoverRadius: 6,
            tension: 0
          };
        })
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: still ? false : { duration: 300 },
        interaction: { mode: "index", intersect: false },
        color: ink,
        scales: {
          x: { ticks: { color: ink, autoSkip: true, maxRotation: 0 }, grid: { color: rule } },
          y: { beginAtZero: true, ticks: { color: ink, precision: 0 }, grid: { color: rule },
               title: { display: true, color: ink, text: picked.show === "open" ? "open at the end of the " + scaleWords[picked.scale] : showWords[picked.show] + " in the " + scaleWords[picked.scale] } }
        },
        plugins: {
          legend: { position: "bottom", labels: { color: ink, usePointStyle: true } },
          tooltip: { usePointStyle: true }
        }
      }
    });
  }

  // ---- controls --------------------------------------------------------------

  ["show", "split", "scale", "splits"].forEach(function (name) {
    controls[name].addEventListener("change", function () {
      if (reading) {
        draw();
      }
    });
  });
  document.getElementById("backlog-controls").addEventListener("submit", function (event) {
    event.preventDefault();
  });
  if (window.matchMedia) {
    var scheme = window.matchMedia("(prefers-color-scheme: dark)");
    var redraw = function () {
      if (reading) {
        draw();
      }
    };
    if (scheme.addEventListener) {
      scheme.addEventListener("change", redraw);
    }
  }

  function start(current) {
    generation += 1;
    reading = null;
    show("loading");
    read(current);
  }

  signin.addEventListener("submit", function (event) {
    event.preventDefault();
    var entered = tokenField.value.trim();
    if (entered === "") {
      return;
    }
    remember(entered);
    start(entered);
  });

  var held = token();
  if (held !== "") {
    start(held);
  } else {
    askForToken("");
  }
})();
