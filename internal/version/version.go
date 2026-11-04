// Package version reports the build's version. Release builds set the
// variables below with -ldflags; local builds fall back to the module's
// build info so `go install` still reports something useful.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set at release time with -ldflags -X.
var (
	// Version is the release version, or "dev" for local builds.
	Version = "dev"
	// Commit is the full Git commit the binary was built from.
	Commit = ""
	// Date is the commit date of that build.
	Date = ""
)

// Info is the version as the CLI and the server report it.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns the version of the running binary.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if info.Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					info.Commit = s.Value
				case "vcs.time":
					info.Date = s.Value
				}
			}
		}
	}
	return info
}

// String is the one-line form printed by `dray version`.
func (i Info) String() string {
	commit := i.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	s := i.Version
	if commit != "" && commit != "none" {
		s += " (" + commit + ")"
	}
	return fmt.Sprintf("%s %s %s/%s", s, i.Go, i.OS, i.Arch)
}
