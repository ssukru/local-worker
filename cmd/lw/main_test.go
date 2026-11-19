package main

import "testing"

func TestParseArgvUpdateFlags(t *testing.T) {
	args, flags := parseArgv([]string{"update", "--to", "v0.3.0", "--yes"})
	if len(args) != 1 || args[0] != "update" {
		t.Fatalf("args = %#v", args)
	}
	if got := flags.Get("to"); got != "v0.3.0" {
		t.Fatalf("--to = %q", got)
	}
	if !flags.Has("yes") {
		t.Fatal("--yes missing")
	}
}

func TestParseArgvDoesNotConsumeNextFlagAsValue(t *testing.T) {
	_, flags := parseArgv([]string{"update", "--to", "--yes"})
	if got := flags.Get("to"); got != "" {
		t.Fatalf("--to = %q", got)
	}
	if !flags.Has("yes") {
		t.Fatal("--yes was consumed as the value of --to")
	}
}

func TestParseArgvTopLevelVersion(t *testing.T) {
	args, flags := parseArgv([]string{"--version"})
	if len(args) != 0 || !flags.Has("version") {
		t.Fatalf("args=%#v version=%v", args, flags.Has("version"))
	}
}
