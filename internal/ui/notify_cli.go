//go:build windows || darwin

package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/StephenSHorton/suzuri/assets"
)

// RunNotifyCLI plays the editor notification sounds, or shows the cards.
// With no arguments it plays success, fail, and published in order.
// `show` raises the same bottom-left cards a terminal program would get.
func RunNotifyCLI(args []string) int {
	if len(args) > 0 && (args[0] == "show" || args[0] == "toast") {
		return runNotifyShow(args[1:])
	}
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
       suzuri notify show [success|fail|published|update] [title] [body]

  notify                 play the three sounds
  notify show            show one card of each kind, with its sound
  notify show fail "Compile failed" "main.go:12"
  notify show update     the permanent update card (click or right-click)
`

func runNotifyShow(args []string) int {
	runtime.LockOSThread()
	if !onMainThread() {
		fmt.Fprintln(os.Stderr, "suzuri notify: cannot show cards from a background thread")
		return 1
	}
	initNoticeApp()
	notes, err := notifyShowArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "suzuri notify:", err)
		fmt.Fprint(os.Stderr, notifyUsage)
		return 2
	}
	fmt.Println("showing", len(notes), "notification(s) — click to dismiss, or wait")
	var last time.Time
	for _, n := range notes {
		fmt.Printf("  %s — %s\n", n.Sound, n.Title)
		postDeskNote(n, false, true)
		// Let each tweet start before the next card, so they don't pile up as one noise.
		until := time.Now().Add(450 * time.Millisecond)
		for time.Now().Before(until) {
			driveNotices(time.Now(), false, true)
			pumpUI(40 * time.Millisecond)
		}
		if n.Never {
			last = time.Now().Add(2 * time.Minute)
		} else if n.Expire > 0 {
			end := time.Now().Add(n.Expire)
			if end.After(last) {
				last = end
			}
		}
	}
	deadline := last.Add(400 * time.Millisecond)
	if deadline.Before(time.Now()) {
		deadline = time.Now().Add(4 * time.Second)
	}
	for time.Now().Before(deadline) {
		driveNotices(time.Now(), false, true)
		if noticeCount() == 0 {
			break
		}
		pumpUI(40 * time.Millisecond)
	}
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
	driveNotices(time.Now(), false, true)
	pumpUI(80 * time.Millisecond)
	return 0
}

func notifyShowArgs(args []string) ([]deskNote, error) {
	if len(args) == 0 {
		return []deskNote{
			sampleNote("success", "Build finished", "3 targets"),
			sampleNote("fail", "Compile failed", "main.go:12"),
			sampleNote("published", "Published", "workshop item is live"),
		}, nil
	}
	kind := strings.ToLower(args[0])
	title, body := "", ""
	switch kind {
	case "success", "info", "system":
		title, body = "Build finished", "3 targets"
	case "fail", "error":
		title, body = "Compile failed", "main.go:12"
	case "published", "warn", "warning", "question":
		title, body = "Published", "workshop item is live"
	case "update":
		n := sampleUpdateNote("")
		if len(args) > 1 {
			n = sampleUpdateNote(strings.TrimPrefix(args[1], "v"))
		}
		return []deskNote{n}, nil
	default:
		return nil, fmt.Errorf("unknown notification %q", args[0])
	}
	if len(args) > 1 {
		title = args[1]
	}
	if len(args) > 2 {
		body = strings.Join(args[2:], " ")
	}
	return []deskNote{sampleNote(kind, title, body)}, nil
}

func sampleNote(kind, title, body string) deskNote {
	n := deskNote{
		Title: title, Body: body, Expire: 4 * time.Second, Focus: false,
	}
	switch strings.ToLower(kind) {
	case "fail", "error":
		n.Sound = "error"
		n.Icon = "error"
		n.Urgency = 2
	case "published", "warn", "warning", "question":
		n.Sound = "question"
	default:
		n.Sound = "info"
	}
	return n
}

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
