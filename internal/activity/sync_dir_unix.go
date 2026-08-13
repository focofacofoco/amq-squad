//go:build !windows

package activity

import "os"

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return f.Sync()
}
