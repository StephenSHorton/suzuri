let meta = #{
    name: "crush-suzuri-research",
    description: "Observe Charm Crush UI architecture, compare to suzuri, speculate, synthesize a Crush-like product plan including settings",
    phases: [
        #{ title: "Observe", detail: "parallel code observers on Crush and suzuri" },
        #{ title: "Speculate", detail: "many product/architecture lenses" },
        #{ title: "Synthesize", detail: "write docs/crush-inspired-plan.md" },
    ],
    when_to_use: "When researching how to make suzuri feel like Charm Crush",
};

let note_schema = #{
    "type": "object",
    "required": ["title", "bullets"],
    "properties": #{
        "title": #{ "type": "string" },
        "bullets": #{
            "type": "array",
            "minItems": 3,
            "maxItems": 10,
            "items": #{ "type": "string" },
        },
    },
};

let idea_schema = #{
    "type": "object",
    "required": ["lens", "ideas", "risks"],
    "properties": #{
        "lens": #{ "type": "string" },
        "ideas": #{
            "type": "array",
            "minItems": 3,
            "maxItems": 8,
            "items": #{ "type": "string" },
        },
        "risks": #{
            "type": "array",
            "minItems": 1,
            "maxItems": 5,
            "items": #{ "type": "string" },
        },
    },
};

let crush = "C:/Users/4step/projects/crush";
let suzuri = "C:/Users/4step/projects/suzuri";

// ---------- Phase 1: Observe ----------
phase("Observe");
log("Spawning Crush/suzuri observers");

let observe_jobs = [];

// Crush UI / Charm stack observers
let crush_topics = [
    #{ id: "styles", path: crush + "/internal/ui/styles", focus: "themes, charmtone, gradients, quickStyle palette, lipgloss v2 usage" },
    #{ id: "dialog", path: crush + "/internal/ui/dialog", focus: "modal dialogs, overlays, quit, models picker, commands palette, forms" },
    #{ id: "model-ui", path: crush + "/internal/ui/model", focus: "root Bubble Tea model, layout, header/sidebar/status/pills, session chrome" },
    #{ id: "chat", path: crush + "/internal/ui/chat", focus: "chat surface, shell integration UI, streaming markdown, glamour" },
    #{ id: "common", path: crush + "/internal/ui/common", focus: "shared widgets buttons scrollbar markdown elements" },
    #{ id: "anim", path: crush + "/internal/ui/anim", focus: "animations and motion language" },
    #{ id: "list", path: crush + "/internal/ui/list", focus: "list components and selection UX" },
    #{ id: "config", path: crush + "/internal/config", focus: "config load store options tui settings persistence JSON schema" },
    #{ id: "shell", path: crush + "/internal/shell", focus: "embedded shell, how commands run, relation to PTY vs app shell" },
    #{ id: "app", path: crush + "/internal/app", focus: "app lifecycle wiring UI to backend" },
    #{ id: "cmd-root", path: crush + "/internal/cmd", focus: "CLI entry, how TUI is launched, windows notes" },
    #{ id: "ui-agents", path: crush + "/internal/ui/AGENTS.md", focus: "UI authoring conventions if present" },
    #{ id: "readme", path: crush + "/README.md", focus: "product positioning features screenshots promises" },
    #{ id: "go-mod", path: crush + "/go.mod", focus: "Charm v2 modules ultraviolet colorprofile charmtone glamour bubbles" },
    #{ id: "completions", path: crush + "/internal/ui/completions", focus: "completion UI patterns" },
    #{ id: "logo", path: crush + "/internal/ui/logo", focus: "branding mark rendering" },
];

for t in crush_topics {
    let p = "";
    p += "You are a read-only code observer. Crush repo root: " + crush + ". ";
    p += "Focus topic: " + t.focus + ". Primary path: " + t.path + ". ";
    p += "Use list_dir, grep, read_file on that path and related imports. ";
    p += "Do NOT guess — open files. Summarize concrete architecture and UX patterns Crush uses that make it feel glamorous and cohesive. ";
    p += "Return JSON: title (short), bullets (3-10 concrete findings with file names).";
    observe_jobs.push(#{
        prompt: p,
        label: "obs-crush-" + t.id,
        capability_mode: "read-only",
        output_schema: note_schema,
    });
}

// Suzuri observers
let suzuri_topics = [
    #{ id: "chrome", path: suzuri + "/internal/chrome", focus: "current Charm chrome tabs palette theme render path" },
    #{ id: "window", path: suzuri + "/internal/ui/window_windows.go", focus: "Win32 host paint ConPTY integration hybrid architecture" },
    #{ id: "config", path: suzuri + "/internal/config", focus: "config surface today vs missing settings" },
    #{ id: "readme", path: suzuri + "/README.md", focus: "product goals status" },
    #{ id: "host", path: suzuri + "/internal/host", focus: "session ConPTY shell hosting" },
    #{ id: "boxdraw", path: suzuri + "/internal/ui/boxdraw_windows.go", focus: "box drawing neon borders paint fidelity" },
];

for t in suzuri_topics {
    let p = "";
    p += "You are a read-only code observer. Suzuri repo: " + suzuri + ". ";
    p += "Focus: " + t.focus + ". Path: " + t.path + ". ";
    p += "Use read_file/grep. Explain how suzuri currently uses Charm and what is NOT Charm (Win32/GDI/VT). ";
    p += "Return JSON title + bullets of concrete findings.";
    observe_jobs.push(#{
        prompt: p,
        label: "obs-suzuri-" + t.id,
        capability_mode: "read-only",
        output_schema: note_schema,
    });
}

let obs = parallel(observe_jobs);
let obs_notes = [];
for r in obs {
    if r != () && r.success && r.output != () {
        obs_notes.push(r.output);
    }
}
log("Observers returned " + obs_notes.len().to_string() + " notes");
write_scratch_file("observe.json", json_encode(obs_notes));

// ---------- Phase 2: Speculate ----------
phase("Speculate");
log("Spawning speculation lenses");

let lenses = [
    "product designer: Crush look-and-feel for a real terminal host",
    "terminal emulator engineer: what Crush does NOT do that suzuri must (PTY VT scrollback)",
    "Charm ecosystem expert: lipgloss v2 bubbles v2 ultraviolet charmtone huh glamour",
    "settings UX designer: in-app settings for font colors themes keybindings",
    "performance engineer: Charm View every paint vs Crush pure TUI",
    "Windows desktop engineer: hybrid GDI host + full Charm overlay risk",
    "accessibility advocate: contrast cursor focus rings keyboard paths",
    "power user: Warp-like productivity without abandoning shell fidelity",
    "minimalist: less chrome more inkstone aesthetic",
    "maximalist: Crush-level dialogs sessions command palette status pills",
    "theme system architect: named themes catppuccin tokyo inkstone crush defaults",
    "config persistence engineer: JSON under LOCALAPPDATA live reload",
    "input routing expert: when palette/settings steal keys from ConPTY",
    "animation skeptic: which Crush anims are worth porting",
    "agent product lead: should suzuri embed AI like Crush or stay a terminal",
    "security reviewer: shell host attack surface vs Crush sandbox",
    "QA lead: crash-on-focus history implications for bigger Charm surface",
    "brand designer: 硯 suzuri identity vs Crush pink gradient language",
    "migration strategist: stepwise roadmap without rewrite",
    "competitor analyst: WT Ghostty WezTerm vs Crush-as-UI inspiration",
    "composer of dialogs: settings as multi-page Bubble Tea model",
    "font engineer: Cascadia size line-height DPI",
    "color engineer: ANSI 16/256 truecolor + UI chrome palette separation",
    "tabs architect: Crush sessions vs terminal tabs",
    "command palette designer: expand beyond 4 actions to full command registry",
    "onboarding designer: first-run Crush-like landing",
    "documentation writer: what to tell users Charm owns",
    "skeptical engineer: reasons NOT to copy Crush wholesale",
];

// Pass observations as JSON to avoid nested field iteration issues.
let digest_enc = json_encode(obs_notes);

let spec_jobs = [];
let li = 0;
for lens in lenses {
    let p = "";
    p += "You are speculating for suzuri (Go real Win32+ConPTY terminal) inspired by Charm Crush. ";
    p += "Lens: " + lens + ". ";
    p += "Read-only tools optional; you may open " + crush + " or " + suzuri + " if needed. ";
    p += "Observation digest (JSON string): " + digest_enc + " ";
    p += "Propose concrete ideas for making suzuri FEEL more like Crush while remaining a real terminal host. ";
    p += "Include settings menu ideas when relevant. Be opinionated. ";
    p += "Return JSON: lens, ideas[], risks[].";
    spec_jobs.push(#{
        prompt: p,
        label: "spec-" + li.to_string(),
        capability_mode: "read-only",
        output_schema: idea_schema,
    });
    li += 1;
}

let specs = parallel(spec_jobs);
let spec_out = [];
for r in specs {
    if r != () && r.success && r.output != () {
        spec_out.push(r.output);
    }
}
log("Speculation lenses: " + spec_out.len().to_string());
write_scratch_file("speculate.json", json_encode(spec_out));

// ---------- Phase 3: Synthesize ----------
phase("Synthesize");

let plan_schema = #{
    "type": "object",
    "required": ["executive_summary", "crush_vs_suzuri", "target_architecture", "settings_design", "roadmap", "non_goals"],
    "properties": #{
        "executive_summary": #{ "type": "string" },
        "crush_vs_suzuri": #{ "type": "string" },
        "target_architecture": #{ "type": "string" },
        "settings_design": #{ "type": "string" },
        "roadmap": #{
            "type": "array",
            "minItems": 4,
            "maxItems": 12,
            "items": #{ "type": "string" },
        },
        "non_goals": #{
            "type": "array",
            "minItems": 2,
            "maxItems": 8,
            "items": #{ "type": "string" },
        },
    },
};

let syn_prompt = "";
syn_prompt += "Synthesize a Crush-inspired product/engineering plan for suzuri. ";
syn_prompt += "Crush path " + crush + ", suzuri path " + suzuri + ". ";
syn_prompt += "You MAY read key files to verify. Observation notes JSON: " + json_encode(obs_notes) + " ";
syn_prompt += "Speculation JSON: " + json_encode(spec_out) + " ";
syn_prompt += "Critical truth: Crush is a Charm Bubble Tea v2 TUI AI coding app that runs INSIDE a host terminal. ";
syn_prompt += "Suzuri IS the host terminal (Win32+ConPTY+VT). Charm cannot replace ConPTY; it should own ALL app chrome and modals. ";
syn_prompt += "User wants: fuller Charm usage like Crush look/feel, settings menu for font colors etc. ";
syn_prompt += "Return structured fields: executive_summary, crush_vs_suzuri, target_architecture, settings_design, roadmap[], non_goals[].";

let syn = agent(syn_prompt, #{
    label: "synthesize-plan",
    capability_mode: "read-only",
    output_schema: plan_schema,
});

if syn == () || !syn.success || syn.output == () {
    complete(#{ error: "synthesis failed", observe_count: obs_notes.len(), spec_count: spec_out.len() });
}

// Write markdown report into suzuri docs via a write-capable agent
let md_prompt = "";
md_prompt += "Write a polished markdown design note to " + suzuri + "/docs/crush-inspired-plan.md using the write tool (overwrite). ";
md_prompt += "Use this structured plan JSON: " + json_encode(syn.output) + " ";
md_prompt += "Also incorporate strongest ideas from speculation JSON (top themes only, not every lens): " + json_encode(spec_out) + " ";
md_prompt += "Structure the doc with: Title, TL;DR, Crush vs Suzuri (table), Target architecture, Settings menu design (fields + UX), Visual language (Crush tokens to steal), Phased roadmap, Non-goals, Open questions. ";
md_prompt += "Be concrete and actionable for implementing next in the Go codebase. Complete sentences.";

let writer = agent(md_prompt, #{
    label: "write-plan-md",
    capability_mode: "read-write",
});

let written = false;
if writer != () && writer.success {
    written = true;
}

complete(#{
    observe_count: obs_notes.len(),
    spec_count: spec_out.len(),
    plan: syn.output,
    doc_written: written,
    doc_path: suzuri + "/docs/crush-inspired-plan.md",
});
