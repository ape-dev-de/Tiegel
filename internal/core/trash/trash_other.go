//go:build (darwin && !cgo) || !unix

package trash

func moveToTrash(string) error { return ErrUnavailable }
