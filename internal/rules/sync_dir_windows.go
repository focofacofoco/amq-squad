package rules

import "os"

func syncDir(dir string) error {
	_, err := os.Stat(dir)
	return err
}
