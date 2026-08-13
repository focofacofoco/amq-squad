//go:build !windows

package cli

func platformLaunchChildCommand(target string, trailing []string) (string, []string) {
	return exactRootChildCommand(target, trailing)
}
