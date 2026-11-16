package langphp

import "os"

func osChmod(p string) error { return os.Chmod(p, 0o755) }
