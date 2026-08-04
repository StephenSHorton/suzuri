package transfer

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// RunCLI handles `suzuri send|receive|transfer …` subcommands.
// Returns a process exit code.
func RunCLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage())
		return 2
	}

	switch args[0] {
	case "send":
		return cliSend(args[1:])
	case "receive", "recv":
		return cliReceive(args[1:])
	case "transfer":
		return cliTransfer(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "suzuri: unknown transfer command %q\n%s\n", args[0], usage())
		return 2
	}
}

// IsTransferArg reports whether os.Args[1] is a transfer-related subcommand.
func IsTransferArg(arg string) bool {
	switch arg {
	case "send", "receive", "recv", "transfer":
		return true
	default:
		return false
	}
}

func usage() string {
	return `usage:
  suzuri send <path>              serve file/folder; print ticket (Ctrl+C when done)
  suzuri receive <ticket> [dir]   download into dir (default: .)
  suzuri transfer version         show engine path + identity
  suzuri transfer me              show local transfer identity`
}

func cliSend(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: suzuri send <path>")
		return 2
	}
	path := args[0]
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if st, err := os.Stat(path); err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "suzuri send: no such file or folder: %s\n", path)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ticketPrinted bool
	c := &Client{
		OnEvent: func(ev Event) {
			switch ev.Event {
			case "ready":
				if ev.Ticket != "" {
					fmt.Println(ev.Ticket)
					ticketPrinted = true
					fmt.Fprintln(os.Stderr, "serving — keep this process open until the peer finishes; Ctrl+C to stop")
				}
			case "progress":
				if ev.Done != nil && ev.Total != nil {
					fmt.Fprintf(os.Stderr, "\rprogress %d / %d", *ev.Done, *ev.Total)
				}
			case "error":
				fmt.Fprintf(os.Stderr, "error: %s\n", ev.Message)
			}
		},
	}
	if err := c.SendTicket(ctx, path, nil, false); err != nil {
		if !ticketPrinted {
			fmt.Fprintf(os.Stderr, "suzuri send: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "\nsuzuri send: %v\n", err)
		}
		return 1
	}
	return 0
}

func cliReceive(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: suzuri receive <ticket> [dir]")
		return 2
	}
	ticket := args[0]
	dir := "."
	if len(args) >= 2 {
		dir = args[1]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := &Client{
		OnEvent: func(ev Event) {
			switch ev.Event {
			case "receiving":
				fmt.Fprintf(os.Stderr, "receiving (relays=%v ips=%v) → %s\n",
					ptrInt(ev.Relays), ptrInt(ev.IPs), dir)
			case "progress":
				if ev.Done != nil && ev.Total != nil && *ev.Total > 0 {
					pct := float64(*ev.Done) * 100 / float64(*ev.Total)
					fmt.Fprintf(os.Stderr, "\r  %6.1f%%  %d / %d", pct, *ev.Done, *ev.Total)
				}
			case "resumed":
				if ev.AlreadyHad != nil {
					fmt.Fprintf(os.Stderr, "\nresumed (%d bytes already had)\n", *ev.AlreadyHad)
				}
			case "done":
				if ev.TotalBytes != nil {
					fmt.Fprintf(os.Stderr, "\nreceived %d bytes into %s\n", *ev.TotalBytes, ev.OutDir)
				} else {
					fmt.Fprintln(os.Stderr, "\ndone")
				}
			case "error":
				fmt.Fprintf(os.Stderr, "error: %s\n", ev.Message)
			}
		},
	}
	if err := c.ReceiveTicket(ctx, ticket, dir); err != nil {
		fmt.Fprintf(os.Stderr, "suzuri receive: %v\n", err)
		return 1
	}
	return 0
}

func cliTransfer(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage())
		return 2
	}
	switch args[0] {
	case "version", "engine":
		bin, err := ResolveBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "suzuri transfer: %v\n", err)
			return 1
		}
		cfg, _ := ConfigDir()
		fmt.Printf("engine: %s\n", bin)
		fmt.Printf("config: %s\n", cfg)
		return 0
	case "me":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		me, err := (&Client{}).Me(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "suzuri transfer me: %v\n", err)
			return 1
		}
		fmt.Printf("name:     %s\n", me.DisplayName)
		fmt.Printf("endpoint: %s\n", me.EndpointID)
		fmt.Printf("short:    %s\n", me.EndpointShort)
		if me.ConfigDir != "" {
			fmt.Printf("config:   %s\n", me.ConfigDir)
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "suzuri transfer: unknown %q\n%s\n", args[0], usage())
		return 2
	}
}

func ptrInt(p *int) any {
	if p == nil {
		return "?"
	}
	return *p
}
