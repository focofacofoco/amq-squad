package activity

import "os"

func syncDir(dir string) error {
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
