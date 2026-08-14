let meta = #{
    name: "pane-management",
    description: "Research suzuri pane/tab/window drag, then recommend a shippable first PR slice",
    phases: [
        #{ title: "Research", detail: "parallel read-only probes of split tree, mouse, windows" },
        #{ title: "Recommend", detail: "synthesize first-slice plan" },
    ],
    when_to_use: "When planning drag-to-rearrange panes, tab tear-off, or cross-window drops",
};

let probe_schema = #{
    "type": "object",
    "required": ["area", "facts", "gaps"],
    "properties": #{
        "area": #{ "type": "string" },
        "facts": #{
            "type": "array",
            "minItems": 3,
            "maxItems": 10,
            "items": #{ "type": "string" },
        },
        "gaps": #{
            "type": "array",
            "minItems": 2,
            "maxItems": 8,
            "items": #{ "type": "string" },
        },
    },
};

let rec_schema = #{
    "type": "object",
    "required": ["first_pr_title", "in_scope", "out_of_scope", "files", "risks"],
    "properties": #{
        "first_pr_title": #{ "type": "string" },
        "in_scope": #{
            "type": "array",
            "minItems": 3,
            "maxItems": 8,
            "items": #{ "type": "string" },
        },
        "out_of_scope": #{
            "type": "array",
            "minItems": 2,
            "maxItems": 6,
            "items": #{ "type": "string" },
        },
        "files": #{
            "type": "array",
            "minItems": 3,
            "maxItems": 10,
            "items": #{ "type": "string" },
        },
        "risks": #{
            "type": "array",
            "minItems": 2,
            "maxItems": 6,
            "items": #{ "type": "string" },
        },
    },
};

let root = if args == () { () } else { args.root };
if root == () {
    pause("verification", "Pass args.root — suzuri repo path (e.g. /Users/stephenhorton/projects/suzuri).");
}

phase("Research");
log("Probing pane tree, mouse routing, and window spawn");

let jobs = [];

let p1 = "";
p1 += "Read-only. Repo: " + root + ". ";
p1 += "Read chrome/src/panes.rs and chrome/src/session.rs with read_file/grep. ";
p1 += "Do not guess. Report how SplitNode split/remove/layout works, how tabs own trees, ";
p1 += "PaneKind/WidgetKind, and what would break if a leaf moved without destroying its PTY. ";
p1 += "Return facts and gaps for drag-to-reparent.";
jobs.push(#{
    prompt: p1,
    label: "probe:tree",
    capability_mode: "read-only",
    output_schema: probe_schema,
});

let p2 = "";
p2 += "Read-only. Repo: " + root + ". ";
p2 += "Read chrome/src/input.rs, chrome/src/layout.rs, and mouse handling in chrome/src/app.rs. ";
p2 += "Use grep for HitTarget, press_hit, CursorMoved, MouseInput. ";
p2 += "Report existing hit targets and where a pane-drag (path/divider grab) can start without ";
p2 += "stealing terminal selection. Facts and gaps only from the code.";
jobs.push(#{
    prompt: p2,
    label: "probe:mouse",
    capability_mode: "read-only",
    output_schema: probe_schema,
});

let p3 = "";
p3 += "Read-only. Repo: " + root + ". ";
p3 += "Read chrome/src/new_window.rs and grep app.rs for NewWindow / spawn_new_window. ";
p3 += "Confirm windows are separate processes. Report what state cannot move across windows ";
p3 += "today (PTY, grids, widget panes). Facts and gaps for tear-off and cross-window drop.";
jobs.push(#{
    prompt: p3,
    label: "probe:windows",
    capability_mode: "read-only",
    output_schema: probe_schema,
});

let probes = parallel(jobs);

let notes = [];
for r in probes {
    if r != () && r.success && r.output.area != () {
        notes.push(r.output);
    }
}
if notes.len() == 0 {
    complete(#{ summary: "Research probes failed; no recommendation.", first_pr_title: "" });
}

phase("Recommend");
log("Synthesizing first-slice recommendation");

let rec_prompt = "";
rec_prompt += "You are recommending the first shippable PR for suzuri pane management. ";
rec_prompt += "Repo: " + root + ". Research notes (JSON): " + json_encode(notes) + ". ";
rec_prompt += "Product: native GPU terminal; jelly splits; workspace is a Widget pane; ";
rec_prompt += "new window is a second process. User wants drag panes, drag between tabs, ";
rec_prompt += "tear-off tabs to a new window, drop tabs on another window. ";
rec_prompt += "Recommend ONE first PR that is independently mergeable. ";
rec_prompt += "In-window re-dock (same tab + onto another tab) is the expected first slice; ";
rec_prompt += "cross-process tear-off is later unless research proves otherwise. ";
rec_prompt += "Fill first_pr_title, in_scope, out_of_scope, files, risks. Be specific.";

let rec = agent(rec_prompt, #{
    label: "recommend:first-pr",
    capability_mode: "read-only",
    output_schema: rec_schema,
});

if rec == () || !rec.success || rec.output.first_pr_title == () {
    complete(#{ summary: "Recommendation agent failed.", probes: notes });
}

let report = "";
report += "# Pane management — first slice\n\n";
report += "## Title\n" + rec.output.first_pr_title + "\n\n";
report += "## In scope\n";
for s in rec.output.in_scope { report += "- " + s + "\n"; }
report += "\n## Out of scope\n";
for s in rec.output.out_of_scope { report += "- " + s + "\n"; }
report += "\n## Files\n";
for s in rec.output.files { report += "- " + s + "\n"; }
report += "\n## Risks\n";
for s in rec.output.risks { report += "- " + s + "\n"; }

let path = write_scratch_file("pane-management.md", report);
complete(#{
    summary: rec.output.first_pr_title,
    path: path,
    first_pr_title: rec.output.first_pr_title,
    in_scope: rec.output.in_scope,
    out_of_scope: rec.output.out_of_scope,
    files: rec.output.files,
    risks: rec.output.risks,
});
