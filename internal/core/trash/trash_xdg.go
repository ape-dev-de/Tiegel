//go:build unix && !darwin

package trash

import "os"

func moveToTrash(path string) error {
	dir, err := homeTrash(os.Getenv("XDG_DATA_HOME"), os.Getenv("HOME"))
	if err != nil {
		return err
	}
	return moveXDG(dir, path)
}
