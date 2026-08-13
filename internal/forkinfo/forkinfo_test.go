package forkinfo

import "testing"

func TestCurrentIdentifiesFACOFork(t *testing.T) {
	got := Current()
	if got.Owner != "focofacofoco" {
		t.Fatalf("Owner = %q", got.Owner)
	}
	if got.Commit == "" {
		t.Fatal("Commit must identify the build")
	}
	if got.UpstreamBase != "63d080fe07c6ebbc2f9e4a7d713e5a0d4228ee63" {
		t.Fatalf("UpstreamBase = %q", got.UpstreamBase)
	}
	if !got.Modified {
		t.Fatal("unstamped development build must report Modified=true")
	}
}
