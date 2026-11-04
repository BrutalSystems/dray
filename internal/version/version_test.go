package version

import (
	"strings"
	"testing"
)

func TestStringShortensCommit(t *testing.T) {
	i := Info{Version: "1.2.3", Commit: "4f9c2e1a8b7c", Go: "go1.26.3", OS: "linux", Arch: "arm64"}
	got := i.String()
	want := "1.2.3 (4f9c2e1) go1.26.3 linux/arm64"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringWithoutCommit(t *testing.T) {
	i := Info{Version: "dev", Go: "go1.26.3", OS: "darwin", Arch: "arm64"}
	if got := i.String(); strings.Contains(got, "(") {
		t.Fatalf("String() = %q, want no commit in parentheses", got)
	}
}

func TestGetUsesLinkedVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "9.9.9"
	if got := Get().Version; got != "9.9.9" {
		t.Fatalf("Get().Version = %q, want 9.9.9", got)
	}
}
