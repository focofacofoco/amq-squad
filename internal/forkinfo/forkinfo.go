// Package forkinfo identifies FACO-owned builds independently from the
// upstream-compatible amq-squad version number.
package forkinfo

const (
	Owner        = "focofacofoco"
	UpstreamBase = "63d080fe07c6ebbc2f9e4a7d713e5a0d4228ee63"
)

// Commit and Modified are stamped by the global installer for trusted builds.
var (
	Commit   = "dev"
	Modified = "true"
)

type Info struct {
	Owner        string
	Commit       string
	UpstreamBase string
	Modified     bool
}

func Current() Info {
	return Info{
		Owner:        Owner,
		Commit:       Commit,
		UpstreamBase: UpstreamBase,
		Modified:     Modified != "false",
	}
}
