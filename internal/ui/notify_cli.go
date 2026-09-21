//go:build windows || darwin

package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/StephenSHorton/suzuri/assets"
)

// RunNotifyCLI plays the editor notification sounds. With no arguments it
// plays success, fail, and published in order so each one can be heard.
func RunNotifyCLI(args []string) int {
	names, err := notifySoundArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "suzuri notify:", err)
		fmt.Fprint(os.Stderr, notifyUsage)
		return 2
	}
	for i, name := range names {
		wav := wavForNotifyName(name)
		if len(wav) == 0 {
			fmt.Fprintf(os.Stderr, "suzuri notify: no audio for %s\n", name)
			return 1
		}
		fmt.Println(name)
		playWAVSync(wav)
		if i+1 < len(names) {
			time.Sleep(350 * time.Millisecond)
		}
	}
	return 0
}

const notifyUsage = `usage: suzuri notify [success|fail|published|all]

  success    the short tweet (normal notifications)
  fail       the short error tweet
  published  the longer confirmation
  all        play all three (default)
`

func notifySoundArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return []string{"success", "fail", "published"}, nil
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return nil, fmt.Errorf("help")
	}
	var out []string
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "", "all":
			out = append(out, "success", "fail", "published")
		case "success", "info", "system":
			out = append(out, "success")
		case "fail", "error":
			out = append(out, "fail")
		case "published", "warn", "warning", "question":
			out = append(out, "published")
		default:
			return nil, fmt.Errorf("unknown sound %q", a)
		}
	}
	return out, nil
}

func wavForNotifyName(name string) []byte {
	switch name {
	case "fail":
		return assets.EditorFailWAV
	case "published":
		return assets.EditorPublishedWAV
	default:
		return assets.EditorSuccessWAV
	}
}
