package host

// EnvShell, when set, replaces the default shell command line for every new
// pane (taken verbatim, no QuietPrompt rewrite). `suzuri stress` uses it to run
// its synthetic renderer in each pane. A profile with an explicit shell still
// wins.
const EnvShell = "SUZURI_SHELL"
