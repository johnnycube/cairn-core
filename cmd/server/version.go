package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version, Commit and BuildTime are injected at build time via
// -ldflags="-X main.Version=... -X main.Commit=... -X main.BuildTime=...".
// When unstamped (e.g. local `go build` from a git checkout) buildInfo()
// falls back to the binary's embedded VCS metadata.
var (
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)

// buildInfo returns the version, commit, and build time, preferring the
// ldflags-injected values and falling back to embedded VCS info.
func buildInfo() (version, commit, buildTime string) {
	version, commit, buildTime = Version, Commit, BuildTime
	if commit == "" || buildTime == "" {
		rev, when, modified := readVCSInfo()
		if commit == "" && rev != "unknown" {
			commit = rev
			if len(commit) > 12 {
				commit = commit[:12]
			}
			if modified {
				commit += "-dirty"
			}
		}
		if buildTime == "" && when != "unknown" {
			buildTime = when
		}
	}
	return version, commit, buildTime
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Cairn binary version and build metadata",
		Run: func(*cobra.Command, []string) {
			vcsRev, vcsTime, modified := readVCSInfo()
			fmt.Printf("cairn version: %s\n", Version)
			fmt.Printf("commit:        %s%s\n", vcsRev, dirtyFlag(modified))
			fmt.Printf("commit time:   %s\n", vcsTime)
			fmt.Printf("go:            %s\n", runtime.Version())
			fmt.Printf("platform:      %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}

// readVCSInfo extracts revision metadata from the binary's embedded build
// info. Works for `go build`-produced binaries from a git checkout without
// the user setting any ldflags.
func readVCSInfo() (rev, when string, modified bool) {
	rev, when = "unknown", "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return
}

func dirtyFlag(modified bool) string {
	if modified {
		return " (modified)"
	}
	return ""
}
