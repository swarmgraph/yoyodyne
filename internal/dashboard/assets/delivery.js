// The delivery page's script: hold the token, read /api/delivery with it, and
// draw what delivery-figures.js makes of it, one panel per measure, each a
// chart and a table of the same figures.
//
// The token is the main page's, held under the same session storage key on
// the same origin, so a tab signed in on the main page opens this one signed
// in. The same rules hold as there (dashboard.js): the token goes in the
// Authorization header of a request to this origin and nowhere else, every
// value goes into the page through textContent, and nothing here sets a style.
//
// The charts are drawn with the backlog page's library, the one copy the
// dashboard serves (chartlib.go), and in the backlog page's way: each series
// told apart by its point shape and line pattern as well as its colour, the
// colours read from the stylesheet so a chart follows the reader's light or
// dark setting. A period with no figure is a gap in the line, never a point
// at zero, and the table under the chart says why. Where the library is not
// served the tables are shown open, alone.
//
// The reading is a snapshot the dashboard builds once a minute, so the page
// asks for it when it opens and every minute after; while the dashboard is
// still building its first, it asks again sooner.

(function () {
  "use strict";

  var storageKey = "yoyo-dashboard-token";
  var every = 60000;
  var soon = 10000;

  var page = document.getElementById("page");
  var signin = document.getElementById("signin");
  var signinNote = document.getElementById("signin-note");
  var tokenField = document.getElementById("token");
  var scale = document.getElementById("delivery-scale");
  var holder = document.getElementById("delivery-measures");

  var reading = null;
  var charts = [];
  var timer = null;
  var generation = 0;

  // Point shapes and line patterns, in step with the palette, so no two of
  // the first several series differ by colour alone. They are the backlog
  // page's (backlog.js).
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

  function make(tag, className, words) {
    var element = document.createElement(tag);
    if (className) {
      element.setAttribute("class", className);
    }
    if (words !== undefined) {
      element.textContent = words;
    }
    return element;
  }

  function empty(element) {
    while (element.firstChild) {
      element.removeChild(element.firstChild);
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
    fetch("/api/delivery", {
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
          schedule(current, every);
        }
      })
      .catch(function (error) {
        if (mine !== generation) {
          return;
        }
        failed(error.message.indexOf("Failed to fetch") >= 0 || error.name === "TypeError"
          ? "the dashboard could not be reached (" + error.message + "); the yoyo dashboard process may have stopped"
          : error.message);
        schedule(current, reading ? every : soon);
      });
  }

  function schedule(current, wait) {
    window.clearTimeout(timer);
    timer = window.setTimeout(function () { read(current); }, wait);
  }

  // failed says why there is no reading. Where an earlier reading is held it
  // stays drawn and the failure is said above it; otherwise the page is the
  // failure.
  function failed(reason) {
    if (reading) {
      var banner = document.getElementById("delivery-stale");
      banner.textContent = "The latest reading failed, so this is the one before it: " + reason;
      setHidden(banner, false);
      return;
    }
    text("problem", reason);
    text("remedy", "This page asks again in ten seconds; reloading it asks now.");
    show("error");
  }

  // ---- drawing ---------------------------------------------------------------

  // zoneWords is the zone the reading counts in, with what it is called at
  // the reading's time, such as "America/Los_Angeles (PDT)".
  function zoneWords(zone, at) {
    try {
      var found = {};
      new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "short" }).formatToParts(new Date(at)).forEach(function (part) {
        found[part.type] = part.value;
      });
      return found.timeZoneName && found.timeZoneName !== zone ? zone + " (" + found.timeZoneName + ")" : zone;
    } catch (error) {
      return zone;
    }
  }

  function browserZone() {
    try {
      return new Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    } catch (error) {
      return "";
    }
  }

  function draw() {
    show("ready");
    var observedAt = Date.parse(reading.observed_at);
    var observed = document.getElementById("observed-at");
    observed.textContent = new Date(observedAt).toLocaleString("en-US", { timeZone: reading.zone || undefined, timeZoneName: "short" });
    observed.setAttribute("datetime", reading.observed_at);

    var zone = reading.zone || "";
    var mine = browserZone();
    var where = "Every day, and every week from Monday to Sunday, is counted in " + zoneWords(zone, observedAt) + ", the time zone of the machine the dashboard runs on.";
    if (mine && zone && mine !== zone) {
      where += " This browser is in " + zoneWords(mine, observedAt) + ", so a day here may not start at your midnight.";
    }
    where += " Durations are the median and the 90th percentile, and the last day or week is still under way.";
    text("delivery-zone", where);

    var stale = document.getElementById("delivery-stale");
    var snapshot = reading.snapshot;
    stale.textContent = snapshot && snapshot.failure
      ? "The dashboard's last reading of the delivery measures failed — " + snapshot.failure + " — so this is the reading taken before it."
      : snapshot && snapshot.stale ? "This reading is older than the dashboard means it to be; its records may have moved on since." : "";
    setHidden(stale, stale.textContent === "");

    var unread = document.getElementById("delivery-sources");
    empty(unread);
    (reading.sources || []).forEach(function (source) {
      if (!source.read) {
        unread.appendChild(make("li", "", "Could not read " + source.name + (source.problem ? ": " + source.problem : "") + ". The measures read from it say so below."));
      }
    });
    setHidden(unread, !unread.firstChild);

    var library = window.Chart;
    setHidden(document.getElementById("delivery-nochart"), !!library);
    dropCharts();
    empty(holder);
    var drawn = deliveryFigures.figures(reading, scale.value);
    drawn.measures.forEach(function (measure) {
      holder.appendChild(panel(measure, drawn.labels, library));
    });
  }

  // panel is one measure: its heading and question, its chart, the notes
  // saying what is not recorded and why, the records it was read from, and
  // its table.
  function panel(measure, labels, library) {
    var section = make("section", "panel");
    section.setAttribute("id", "delivery-" + measure.key);
    section.setAttribute("aria-labelledby", "delivery-" + measure.key + "-heading");
    var head = make("div", "panel-head");
    var heading = make("h2", "", measure.heading);
    heading.setAttribute("id", "delivery-" + measure.key + "-heading");
    head.appendChild(heading);
    head.appendChild(make("p", "panel-note", measure.question));
    section.appendChild(head);

    if (library && measure.drawn) {
      var box = make("div", "delivery-chart");
      var canvas = make("canvas");
      canvas.setAttribute("id", "delivery-" + measure.key + "-canvas");
      canvas.setAttribute("role", "img");
      canvas.setAttribute("aria-label", "The chart of " + measure.heading.toLowerCase() + "; the same figures are in the table below it");
      box.appendChild(canvas);
      section.appendChild(box);
      charts.push(drawChart(library, canvas, measure, labels));
    } else if (!measure.drawn) {
      section.appendChild(make("p", "empty", "No " + (scale.value === "weekly" ? "week" : "day") + " drawn has a figure for this measure; the table says why for each."));
    }

    var notes = make("ul", "footnotes");
    measure.notes.forEach(function (line) {
      notes.appendChild(make("li", "", line));
    });
    if (measure.readFrom.length) {
      notes.appendChild(make("li", "", "Read from " + measure.readFrom.join(", ") + "."));
    }
    section.appendChild(notes);

    var figures = make("details", "backlog-figures");
    if (!library || !measure.drawn) {
      figures.setAttribute("open", "");
    }
    figures.appendChild(make("summary", "", "The figures as a table"));
    var wrap = make("div", "backlog-table-wrap");
    wrap.appendChild(table(measure));
    figures.appendChild(wrap);
    section.appendChild(figures);
    return section;
  }

  // table is the same figures as the chart, newest period first.
  function table(measure) {
    var element = make("table", "backlog-table");
    var head = make("thead");
    var headRow = make("tr");
    measure.columns.forEach(function (name) {
      var cell = make("th", "", name);
      cell.setAttribute("scope", "col");
      headRow.appendChild(cell);
    });
    head.appendChild(headRow);
    element.appendChild(head);
    var body = make("tbody");
    measure.rows.forEach(function (cells) {
      var row = make("tr");
      cells.forEach(function (words, index) {
        var cell = make(index === 0 ? "th" : "td", "", words);
        if (index === 0) {
          cell.setAttribute("scope", "row");
        }
        row.appendChild(cell);
      });
      body.appendChild(row);
    });
    element.appendChild(body);
    return element;
  }

  function dropCharts() {
    charts.forEach(function (chart) {
      chart.destroy();
    });
    charts = [];
  }

  // tone is one of the stylesheet's colours, so the chart's text and rules
  // follow the reader's light or dark setting.
  function tone(name) {
    return window.getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function drawChart(library, canvas, measure, labels) {
    var still = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    var ink = tone("--ink-soft"), rule = tone("--rule");
    var y = { beginAtZero: true, ticks: { color: ink }, grid: { color: rule }, title: { display: true, color: ink, text: measure.axis } };
    if (measure.unit === "count") {
      y.ticks.precision = 0;
    }
    if (measure.unit === "percent") {
      y.suggestedMax = 100;
    }
    return new library(canvas, {
      type: "line",
      data: {
        labels: labels,
        datasets: measure.series.map(function (each, index) {
          var colour = palette[index % palette.length];
          return {
            label: each.name,
            data: each.values,
            borderColor: colour,
            backgroundColor: colour,
            borderWidth: 2,
            borderDash: dashes[index % dashes.length],
            pointStyle: shapes[index % shapes.length],
            pointRadius: 3,
            pointHoverRadius: 6,
            spanGaps: false,
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
          y: y
        },
        plugins: {
          legend: { position: "bottom", labels: { color: ink, usePointStyle: true } },
          tooltip: { usePointStyle: true }
        }
      }
    });
  }

  // ---- controls --------------------------------------------------------------

  scale.addEventListener("change", function () {
    if (reading) {
      draw();
    }
  });
  document.getElementById("delivery-controls").addEventListener("submit", function (event) {
    event.preventDefault();
  });
  if (window.matchMedia) {
    var scheme = window.matchMedia("(prefers-color-scheme: dark)");
    if (scheme.addEventListener) {
      scheme.addEventListener("change", function () {
        if (reading) {
          draw();
        }
      });
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
