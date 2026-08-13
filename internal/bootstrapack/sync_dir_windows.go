package bootstrapack

import "os"

func syncMarkerDir(dir string) error {
	_, err := os.Stat(dir)
	return err
}
