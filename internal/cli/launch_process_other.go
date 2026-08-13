//go:build !windows

package cli

import "github.com/omriariav/amq-squad/v2/internal/launch"

func runPlatformAgent(_ string, _ []string, _ launch.Record, _ string, _ *launchRecordWriteSnapshot) (bool, bool, error) {
	return false, false, nil
}
