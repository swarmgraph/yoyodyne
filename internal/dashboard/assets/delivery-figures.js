// The delivery page's figures: from the reading /api/delivery serves to what
// the page draws for each measure — the series its chart plots and the rows of
// its table. It touches no document, so the page's script draws what this
// returns and testdata/delivery-figures.test.js runs it under Node.
//
// The days and weeks are the reading's own, counted by the dashboard in the
// zone the reading names (readmodel/delivery.go); nothing here moves a figure
// between periods, it only names each period in words.
//
// A figure the reading does not carry is never drawn as zero. Every value is a
// number or null, and null is a gap in the chart's line. The table says which
// of three things the gap is, in words: "not recorded", where the measure's
// records could not be read for that period, with the reason in the notes
// under the chart; "nothing merged", where there was no change to measure; or
// the measure's own words for a period that had changes but nothing to take
// the figure over, such as no change whose event logs record both its first
// checks and its first review.

var deliveryFigures = (function () {
  "use strict";

  var notRecorded = "not recorded";
  var nothingMerged = "nothing merged";

  // The stages of lead time in the order a change passes through them, in
  // the words the page uses for each.
  var stageWords = {
    queued: "waiting for its first run",
    developing: "developing",
    checks: "running checks",
    review: "under review",
    repairing: "repairing",
    merging: "merging"
  };
  var stageOrder = ["queued", "developing", "checks", "review", "repairing", "merging"];

  var months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  var weekdays = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

  // periodWords is a period's date, as the reading labels it (2026-10-05, the
  // day or the Monday a week starts on), in words: "Mon 5 Oct" or "week of
  // Mon 5 Oct", with the year where it is not the year of the reading, and
  // "so far" on the period the reading was taken in. The date is a calendar
  // date, so it is read as one, whatever zone the browser is in.
  function periodWords(period, scale, readingYear) {
    var found = /^(\d{4})-(\d{2})-(\d{2})$/.exec(period.label || "");
    if (!found) {
      return period.label || "";
    }
    var at = new Date(Date.UTC(Number(found[1]), Number(found[2]) - 1, Number(found[3])));
    var words = weekdays[at.getUTCDay()] + " " + at.getUTCDate() + " " + months[at.getUTCMonth()];
    if (found[1] !== readingYear) {
      words += " " + found[1];
    }
    if (scale === "weekly") {
      words = "week of " + words;
    }
    return period.current ? words + ", so far" : words;
  }

  // duration is seconds in words: "45 s", "12 min", "3 h 5 min", "2 d 4 h".
  function duration(seconds) {
    if (seconds === null || seconds === undefined) {
      return "";
    }
    var whole = Math.max(0, Math.round(seconds));
    if (whole < 60) {
      return whole + " s";
    }
    var minutes = Math.round(whole / 60);
    if (minutes < 60) {
      return minutes + " min";
    }
    var hours = Math.floor(minutes / 60);
    if (hours < 48) {
      return hours + " h" + (minutes % 60 ? " " + (minutes % 60) + " min" : "");
    }
    var days = Math.floor(hours / 24);
    return days + " d" + (hours % 24 ? " " + (hours % 24) + " h" : "");
  }

  // checkDuration is a check's time in words, to the second under an hour,
  // because a change to a check is often a minute or less: "45 s", "3 min
  // 30 s", "1 h 5 min".
  function checkDuration(seconds) {
    var whole = Math.max(0, Math.round(seconds));
    if (whole < 60) {
      return whole + " s";
    }
    if (whole < 3600) {
      return Math.floor(whole / 60) + " min" + (whole % 60 ? " " + (whole % 60) + " s" : "");
    }
    return duration(whole);
  }

  // hours is seconds as hours for the chart, to two decimal places.
  function hours(seconds) {
    return Math.round(seconds / 36) / 100;
  }

  function percent(rate) {
    return Math.round(rate * 1000) / 10;
  }

  function percentWords(rate) {
    return percent(rate) + "%";
  }

  function tokenWords(count) {
    return Math.round(count).toLocaleString("en-US");
  }

  function plural(count, one, many) {
    return count + " " + (count === 1 ? one : many);
  }

  function merges(period) {
    var merged = period.merges || {};
    return merged.not_recorded || merged.count === undefined || merged.count === null ? null : merged.count;
  }

  // gap is the words for a period a measure carries no figure for: its own
  // reason it was not recorded, nothing merged, or the measure's own words.
  function gap(measure, period, otherwise) {
    if (measure.not_recorded) {
      return notRecorded;
    }
    if (merges(period) === 0) {
      return nothingMerged;
    }
    return otherwise;
  }

  // Each measure: its heading, the question it answers, what its chart's
  // axis counts, its series, and its table's columns and cells. series(period)
  // is one value per series name, null where there is no figure; cells(period)
  // is the table's words for the period, one per column.
  var measures = [
    {
      key: "merges",
      heading: "Changes merged",
      question: "How often does a change reach main?",
      axis: "changes merged",
      unit: "count",
      of: function (period) { return period.merges || {}; },
      names: function () { return ["changes merged"]; },
      series: function (period) { return [merges(period)]; },
      columns: ["changes merged"],
      cells: function (period) {
        var count = merges(period);
        return [count === null ? notRecorded : String(count)];
      }
    },
    {
      key: "lead_time",
      heading: "Lead time",
      question: "How long does a change take, from the item being filed to its merge?",
      axis: "hours from filing to merge",
      unit: "hours",
      of: function (period) { return period.lead_time || {}; },
      names: function () { return ["median", "90th percentile"]; },
      series: function (period) {
        var total = this.of(period).total;
        if (this.of(period).not_recorded || !total) {
          return [null, null];
        }
        return [hours(total.median_seconds), hours(total.p90_seconds)];
      },
      columns: ["median", "90th percentile", "changes timed"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || !measure.total) {
          var why = gap(measure, period, "no filing time recorded");
          return [why, why, measure.not_recorded ? notRecorded : "0"];
        }
        return [duration(measure.total.median_seconds), duration(measure.total.p90_seconds), String(measure.total.count)];
      },
      notes: function (period) {
        var measure = this.of(period);
        var lines = [];
        if (measure.filing_unknown) {
          lines.push(plural(measure.filing_unknown, "change has", "changes have") + " no filing time in the tracker and " + (measure.filing_unknown === 1 ? "is" : "are") + " not timed");
        }
        return lines;
      }
    },
    {
      key: "stages",
      heading: "Lead time by stage",
      question: "Where does that time go?",
      axis: "median hours in the stage",
      unit: "hours",
      of: function (period) { return period.lead_time || {}; },
      names: function () { return stageOrder.map(function (stage) { return stageWords[stage]; }); },
      stage: function (period, name) {
        var stages = this.of(period).stages || [];
        for (var index = 0; index < stages.length; index++) {
          if (stages[index].stage === name) {
            return stages[index];
          }
        }
        return null;
      },
      series: function (period) {
        var measure = this.of(period);
        var self = this;
        return stageOrder.map(function (name) {
          var stage = self.stage(period, name);
          return measure.not_recorded || !stage || !stage.duration ? null : hours(stage.duration.median_seconds);
        });
      },
      columns: stageOrder.map(function (stage) { return stageWords[stage]; }),
      cells: function (period) {
        var measure = this.of(period);
        var self = this;
        return stageOrder.map(function (name) {
          var stage = self.stage(period, name);
          if (measure.not_recorded || !stage) {
            return gap(measure, period, notRecorded);
          }
          if (!stage.duration) {
            return stage.not_recorded === nothingMerged || merges(period) === 0 ? nothingMerged : notRecorded;
          }
          return "median " + duration(stage.duration.median_seconds) + ", 90th percentile " + duration(stage.duration.p90_seconds);
        });
      },
      // A stage's own reason it was not recorded is said in the notes, as a
      // measure's is.
      reasons: function (period) {
        var measure = this.of(period);
        var self = this;
        if (measure.not_recorded || merges(period) === 0) {
          return [];
        }
        return stageOrder.map(function (name) {
          var stage = self.stage(period, name);
          return stage && !stage.duration && stage.not_recorded ? stageWords[name] + ": " + stage.not_recorded : "";
        }).filter(Boolean);
      },
      notes: function (period) {
        var measure = this.of(period);
        var lines = [];
        if (measure.stages_unknown) {
          lines.push(plural(measure.stages_unknown, "change's", "changes'") + " event logs record nothing, so only " + (measure.stages_unknown === 1 ? "its" : "their") + " wait for a first run is timed");
        }
        if (measure.review_rounds) {
          lines.push("review rounds per change: median " + measure.review_rounds.median + ", most " + measure.review_rounds.max);
        }
        if (measure.repair_rounds) {
          lines.push("repairs per change: median " + measure.repair_rounds.median + ", most " + measure.repair_rounds.max);
        }
        return lines;
      }
    },
    {
      key: "change_failure",
      heading: "Change failure rate",
      question: "How often does a merged change turn out broken: reverted, failing its checks on main, or cited by a bug filed within 48 hours?",
      axis: "% of changes merged",
      unit: "percent",
      of: function (period) { return period.change_failure || {}; },
      names: function () { return ["failed"]; },
      series: function (period) {
        var measure = this.of(period);
        return [measure.not_recorded || measure.rate === undefined || measure.rate === null ? null : percent(measure.rate)];
      },
      columns: ["failed", "of changes merged"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || measure.rate === undefined || measure.rate === null) {
          var why = gap(measure, period, nothingMerged);
          return [why, measure.not_recorded ? notRecorded : String(measure.merges || 0)];
        }
        return [percentWords(measure.rate) + " (" + measure.failed + ")", String(measure.merges)];
      },
      notes: function (period) {
        var measure = this.of(period);
        var lines = [];
        if (measure.failed) {
          var ways = [];
          if (measure.reverted) { ways.push(measure.reverted + " reverted"); }
          if (measure.red_after_merge) { ways.push(measure.red_after_merge + " failed checks on main"); }
          if (measure.defect_within_48h) { ways.push(measure.defect_within_48h + " cited by a bug within 48 hours"); }
          lines.push("failed: " + ways.join(", ") + (measure.failed_changes ? " (" + measure.failed_changes.map(function (each) { return each.work_item_id; }).join(", ") + ")" : ""));
        }
        if (measure.settling) {
          lines.push(plural(measure.settling, "change", "changes") + " merged less than 48 hours ago, so a bug may still be filed against " + (measure.settling === 1 ? "it" : "them"));
        }
        if (measure.landing_unchecked) {
          lines.push(plural(measure.landing_unchecked, "change has", "changes have") + " no recorded result of the checks on its merge commit");
        }
        return lines;
      }
    },
    {
      key: "recovery",
      heading: "Recovery time",
      question: "When the harness itself breaks, how long does it stay broken?",
      axis: "hours broken",
      unit: "hours",
      of: function (period) { return period.recovery || {}; },
      names: function () { return ["median", "90th percentile"]; },
      series: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || !measure.recovered) {
          return [null, null];
        }
        return [hours(measure.recovered.median_seconds), hours(measure.recovered.p90_seconds)];
      },
      columns: ["median", "90th percentile", "breakages cleared"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded) {
          return [notRecorded, notRecorded, notRecorded];
        }
        if (!measure.recovered) {
          return ["nothing cleared", "nothing cleared", "0"];
        }
        return [duration(measure.recovered.median_seconds), duration(measure.recovered.p90_seconds), String(measure.recovered.count)];
      },
      notes: function (period) {
        var measure = this.of(period);
        return (measure.still_broken || []).map(function (each) {
          return "still broken: " + each.kind + (each.subject ? " (" + each.subject + ")" : "") + ", for " + duration(each.seconds);
        });
      }
    },
    {
      key: "rework",
      heading: "Rework rate",
      question: "What share of the changes merged were bug fixes?",
      axis: "% of changes merged",
      unit: "percent",
      of: function (period) { return period.rework || {}; },
      names: function () { return ["bug fixes", "no kind recorded"]; },
      series: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded) {
          return [null, null];
        }
        return [
          measure.rate === undefined || measure.rate === null ? null : percent(measure.rate),
          measure.untyped_share === undefined || measure.untyped_share === null ? null : percent(measure.untyped_share)
        ];
      },
      columns: ["bug fixes", "no kind recorded", "of changes merged"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || measure.rate === undefined || measure.rate === null) {
          var why = gap(measure, period, nothingMerged);
          return [why, why, measure.not_recorded ? notRecorded : String(measure.merges || 0)];
        }
        var rate = percentWords(measure.rate) + " (" + measure.bugs + ")";
        return [measure.minimum ? "at least " + rate : rate, percentWords(measure.untyped_share || 0) + " (" + measure.untyped + ")", String(measure.merges)];
      },
      notes: function (period) {
        var measure = this.of(period);
        return measure.minimum ? ["the rate is " + measure.minimum] : [];
      }
    },
    {
      key: "hand_steps",
      heading: "Hand steps per merged change",
      question: "How often did the operator have to step in, per change merged?",
      axis: "recorded hand steps per change",
      unit: "per change",
      of: function (period) { return period.hand_steps || {}; },
      names: function () { return ["recorded hand steps per change"]; },
      series: function (period) {
        var count = this.of(period).count;
        return [this.of(period).not_recorded || !count || !count.merged ? null : Math.round(count.per_change * 100) / 100];
      },
      columns: ["per change, at least", "hand steps naming a change", "naming none"],
      cells: function (period) {
        var measure = this.of(period);
        var count = measure.count;
        if (measure.not_recorded || !count || !count.merged) {
          var why = gap(measure, period, nothingMerged);
          return [why, measure.not_recorded ? notRecorded : String(count ? count.attributed : 0), measure.not_recorded ? notRecorded : String(count ? count.unattributed : 0)];
        }
        return [String(Math.round(count.per_change * 100) / 100), String(count.attributed), String(count.unattributed)];
      },
      notes: function (period) {
        var count = this.of(period).count;
        return count && count.floor ? ["the count is " + count.floor] : [];
      }
    },
    {
      key: "first_pass",
      heading: "First-pass rate",
      question: "What share of the changes merged passed their first checks and their first review?",
      axis: "% of changes timed",
      unit: "percent",
      of: function (period) { return period.first_pass || {}; },
      names: function () { return ["passed first time"]; },
      series: function (period) {
        var measure = this.of(period);
        return [measure.not_recorded || measure.rate === undefined || measure.rate === null ? null : percent(measure.rate)];
      },
      columns: ["passed first time", "changes whose logs record both"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || measure.rate === undefined || measure.rate === null) {
          var why = gap(measure, period, "no change's logs record both");
          return [why, measure.not_recorded ? notRecorded : String(measure.known || 0)];
        }
        return [percentWords(measure.rate) + " (" + measure.passed + ")", String(measure.known)];
      },
      notes: function (period) {
        var measure = this.of(period);
        return measure.unknown ? [plural(measure.unknown, "change's", "changes'") + " event logs do not record both, and the rate is not taken over " + (measure.unknown === 1 ? "it" : "them")] : [];
      }
    },
    {
      key: "tokens",
      heading: "Tokens per merged change, by model",
      question: "How many tokens did each change merged cost, model by model?",
      axis: "thousand tokens per change",
      unit: "thousand tokens",
      of: function (period) { return period.tokens || {}; },
      // The models are every one any period names, in order of their name.
      names: function (periods) {
        var seen = {};
        periods.forEach(function (period) {
          ((period.tokens || {}).by_model || []).forEach(function (each) { seen[each.model] = true; });
        });
        return Object.keys(seen).sort();
      },
      series: function (period, names) {
        var measure = this.of(period);
        if (measure.not_recorded || !measure.recorded) {
          return names.map(function () { return null; });
        }
        // A model that worked on none of the period's changes cost them
        // nothing, which is a true zero.
        return names.map(function (name) {
          var found = (measure.by_model || []).filter(function (each) { return each.model === name; })[0];
          return found ? Math.round(found.per_change.total / 100) / 10 : 0;
        });
      },
      columns: ["per change, by model", "changes with usage recorded", "without"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded || !measure.recorded) {
          var why = gap(measure, period, "no usage recorded");
          return [why, measure.not_recorded ? notRecorded : "0", measure.not_recorded ? notRecorded : String(measure.unrecorded || 0)];
        }
        var models = (measure.by_model || []).map(function (each) { return each.model + ": " + tokenWords(each.per_change.total); }).join("; ");
        return [models, String(measure.recorded), String(measure.unrecorded || 0)];
      },
      notes: function (period) {
        var measure = this.of(period);
        return measure.unrecorded ? [plural(measure.unrecorded, "change has", "changes have") + " no usage recorded and " + (measure.unrecorded === 1 ? "is" : "are") + " not counted in the figures per change"] : [];
      }
    },
    {
      // Every run of a heavy check, merged or not, in the period it launched
      // in (readmodel/checktimes.go): a period with none ran nothing, which is
      // a true zero, and its medians are gaps.
      key: "check_times",
      heading: "Check times",
      question: "How long did each heavy check take when it passed, and how long did it wait for a free place to run?",
      axis: "median minutes",
      unit: "minutes",
      of: function (period) { return period.check_times || {}; },
      // Two series per check, every check any period names, in order of name.
      names: function (periods) {
        var seen = {};
        periods.forEach(function (period) {
          ((period.check_times || {}).checks || []).forEach(function (each) { seen[each.check] = true; });
        });
        var names = [];
        Object.keys(seen).sort().forEach(function (check) {
          names.push(check + ", passed");
          names.push(check + ", waiting");
        });
        return names;
      },
      series: function (period, names) {
        var measure = this.of(period);
        return names.map(function (name) {
          if (measure.not_recorded) {
            return null;
          }
          var waiting = / waiting$/.test(name);
          var check = name.replace(waiting ? /, waiting$/ : /, passed$/, "");
          var found = (measure.checks || []).filter(function (each) { return each.check === check; })[0];
          var summary = found && (waiting ? found.waited : found.passed_ran);
          return summary ? Math.round(summary.median_seconds / 6) / 10 : null;
        });
      },
      columns: ["ran, and passed", "median time when passed", "median wait for a free place", "told to run the whole module"],
      cells: function (period) {
        var measure = this.of(period);
        if (measure.not_recorded) {
          return [notRecorded, notRecorded, notRecorded, notRecorded];
        }
        var checks = measure.checks || [];
        if (!checks.length) {
          return ["no heavy check ran", "", "", ""];
        }
        function each(words) {
          return checks.map(function (check) { return check.check + ": " + words(check); }).join("; ");
        }
        return [
          each(function (check) { return check.launches + " (" + check.passed + ")"; }),
          each(function (check) { return check.passed_ran ? checkDuration(check.passed_ran.median_seconds) : "none passed"; }),
          each(function (check) { return check.waited ? checkDuration(check.waited.median_seconds) : ""; }),
          each(function (check) {
            var reasons = (check.whole_module_reasons || []).map(function (reason) { return reason.count + " because " + reason.reason; });
            return check.whole_module + (reasons.length ? " (" + reasons.join("; ") + ")" : "");
          })
        ];
      },
      notes: function (period) {
        var measure = this.of(period);
        var lines = [];
        (measure.checks || []).forEach(function (check) {
          if (check.narrowed) {
            lines.push(check.check + ": " + plural(check.narrowed, "run was", "runs were") + " told to run only the packages the change touches");
          }
          if (check.scope_not_recorded) {
            lines.push(check.check + ": what " + plural(check.scope_not_recorded, "run was", "runs were") + " told to run is not recorded");
          }
        });
        if ((measure.checks || []).length) {
          lines.push("what a run was told is what the harness told it; a project's own check may still run the whole module for a reason of its own, which only its output says");
        }
        return lines;
      }
    }
  ];

  // grouped is one note per distinct reason, naming how many periods it held
  // for and which: "not recorded on 3 days (Mon 5 Oct, ...): why".
  function grouped(entries, scale, lead) {
    var order = [];
    var byWhy = {};
    entries.forEach(function (entry) {
      if (!byWhy[entry.why]) {
        byWhy[entry.why] = [];
        order.push(entry.why);
      }
      byWhy[entry.why].push(entry.label);
    });
    var unit = scale === "weekly" ? ["week", "weeks"] : ["day", "days"];
    return order.map(function (why) {
      var labels = byWhy[why];
      var named = labels.length > 3 ? labels.slice(0, 3).join("; ") + "; and " + (labels.length - 3) + " more" : labels.join("; ");
      return lead + " " + plural(labels.length, unit[0], unit[1]) + " (" + named + "): " + why;
    });
  }

  // figures is everything the page draws for one scale, "daily" or
  // "weekly": the periods' labels, oldest first, and per measure its series,
  // its table rows newest first, and its notes.
  function figures(reading, scale) {
    var periods = (scale === "weekly" ? reading.weeks : reading.days) || [];
    var readingYear = String(reading.observed_at || "").slice(0, 4);
    var labels = periods.map(function (period) { return periodWords(period, scale, readingYear); });
    return {
      zone: reading.zone || "",
      labels: labels,
      measures: measures.map(function (measure) {
        var names = measure.names(periods);
        var series = names.map(function (name) { return { name: name, values: [] }; });
        var rows = [];
        var missing = [];
        var partial = [];
        var counted = {};
        var readFrom = [];
        periods.forEach(function (period, index) {
          var of = measure.of(period);
          var values = measure.series(period, names);
          values.forEach(function (value, at) { series[at].values.push(value); });
          rows.unshift([labels[index]].concat(measure.cells(period)));
          (of.read_from || []).forEach(function (source) {
            if (readFrom.indexOf(source) < 0) {
              readFrom.push(source);
            }
          });
          if (of.not_recorded) {
            missing.push({ label: labels[index], why: of.not_recorded });
          }
          (measure.reasons ? measure.reasons(period) : []).forEach(function (why) {
            missing.push({ label: labels[index], why: why });
          });
          (of.not_read || []).forEach(function (why) {
            partial.push({ label: labels[index], why: why });
          });
          // The period's own notes are said once each, with how many
          // periods carried them, on the newest period's wording.
          (measure.notes ? measure.notes(period) : []).forEach(function (line) {
            counted[line] = (counted[line] || 0) + 1;
          });
        });
        var notes = grouped(missing, scale, "Not recorded on")
          .concat(grouped(partial, scale, "Read from only some of its records, so a floor, on"));
        Object.keys(counted).forEach(function (line) {
          notes.push(line.charAt(0).toUpperCase() + line.slice(1) + (counted[line] > 1 ? " (" + plural(counted[line], scale === "weekly" ? "week" : "day", scale === "weekly" ? "weeks" : "days") + ")" : "") + ".");
        });
        return {
          key: measure.key,
          heading: measure.heading,
          question: measure.question,
          axis: measure.axis,
          unit: measure.unit,
          series: series,
          columns: [scale === "weekly" ? "week" : "day"].concat(measure.columns),
          rows: rows,
          notes: notes,
          readFrom: readFrom,
          // drawn is whether any period has a figure at all; a measure with
          // none draws no chart, only its table and why.
          drawn: series.some(function (each) { return each.values.some(function (value) { return value !== null; }); })
        };
      })
    };
  }

  return {
    figures: figures,
    measures: measures.map(function (measure) { return measure.key; }),
    duration: duration,
    periodWords: periodWords,
    notRecorded: notRecorded,
    nothingMerged: nothingMerged
  };
})();

if (typeof module !== "undefined" && module.exports) {
  module.exports = deliveryFigures;
}
