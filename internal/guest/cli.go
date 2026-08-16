package guest

import (
	"fmt"
	"os"
	"strings"
)

// IsArg reports whether os.Args[1] is the guest command.
func IsArg(arg string) bool {
	return arg == "guest"
}

// RunCLI handles `suzuri guest …`. Returns a process exit code.
func RunCLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage())
		return 2
	}
	switch args[0] {
	case "install":
		return cliInstall(args[1:])
	case "remove", "uninstall", "rm":
		return cliRemove(args[1:])
	case "list", "ls":
		return cliList()
	case "help", "-h", "--help":
		fmt.Println(usage())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "suzuri guest: unknown command %q\n%s\n", args[0], usage())
		return 2
	}
}

func usage() string {
	return `usage:
  suzuri guest install ladybird [--from <Ladybird.app|zip>]
  suzuri guest remove ladybird
  suzuri guest list

Installs live under the product config dir (guests/). Chrome reads the
manifest; missing guests are a no-op. Ladybird is not shipped inside suzuri.`
}

func cliInstall(args []string) int {
	id := "ladybird"
	from := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--from" && i+1 < len(args):
			from = args[i+1]
			i++
		case strings.HasPrefix(a, "--from="):
			from = strings.TrimPrefix(a, "--from=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "suzuri guest install: unknown flag %s\n", a)
			return 2
		default:
			id = a
		}
	}
	m, err := install(id, InstallOptions{From: from})
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri guest install: %v\n", err)
		return 1
	}
	fmt.Printf("installed %s\n  command  %s\n  manifest %s\n", m.ID, m.Command, ManifestPath(m.ID))
	fmt.Println("open a pane from the palette: New guest pane")
	return 0
}

func cliRemove(args []string) int {
	id := "ladybird"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id = args[0]
	}
	if err := remove(id); err != nil {
		fmt.Fprintf(os.Stderr, "suzuri guest remove: %v\n", err)
		return 1
	}
	fmt.Printf("removed %s\n", id)
	return 0
}

func cliList() int {
	cat := loadCatalog()
	have := map[string]Manifest{}
	for _, m := range listInstalled() {
		have[m.ID] = m
	}
	if len(cat.Guests) == 0 && len(have) == 0 {
		fmt.Println("no guests")
		return 0
	}
	seen := map[string]bool{}
	for _, g := range cat.Guests {
		seen[g.ID] = true
		status := "not installed"
		if m, ok := have[g.ID]; ok {
			status = "installed"
			if m.Command != "" {
				status += "  " + m.Command
			}
		}
		fmt.Printf("%-12s  %s\n", g.ID, status)
	}
	for _, m := range have {
		if seen[m.ID] {
			continue
		}
		fmt.Printf("%-12s  installed  %s\n", m.ID, m.Command)
	}
	return 0
}
