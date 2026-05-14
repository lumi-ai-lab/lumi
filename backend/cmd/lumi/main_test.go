package main

import "testing"

func TestRunRoutesCLICommands(t *testing.T) {
	if err := run([]string{"cron", "--help"}); err != nil {
		t.Fatalf("run cron help error = %v", err)
	}
	if err := run([]string{"setup", "--help"}); err != nil {
		t.Fatalf("run setup help error = %v", err)
	}
	if err := run([]string{"wecom", "--help"}); err != nil {
		t.Fatalf("run wecom help error = %v", err)
	}
}

func TestRunRejectsUnknownBareCommand(t *testing.T) {
	if err := run([]string{"bogus"}); err == nil {
		t.Fatal("run bogus error = nil, want error")
	}
}
