package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/shellenv"
)

// Binary sources reported by Find.
const (
	SourceSetting    = "setting"     // the docker.path setting
	SourcePath       = "path"        // the process PATH
	SourceLoginShell = "login-shell" // the PATH of the user's login shell
	SourceCommon     = "common"      // a well-known install directory
)

// Binary is a docker executable found by Find.
type Binary struct {
	Path string `json:"path"`
	// Source tells how it was found (Source* constants).
	Source string `json:"source"`
}

// FindOptions control Find.
type FindOptions struct {
	// Configured is the docker.path setting: an absolute path or a
	// command name. When set, nothing else is searched.
	Configured string
	// ProcessPath is the PATH to search first; empty means
	// os.Getenv("PATH").
	ProcessPath string
	// LoginPath returns the login shell's PATH (shellenv). It is only
	// asked when the process PATH has no docker; nil skips it.
	LoginPath func(ctx context.Context) (string, error)
	// CommonDirs replaces the well-known directories (nil: CommonDirs for
	// the running OS and the user's home).
	CommonDirs []string
}

// CommonDirs returns the directories docker installers put the CLI in:
// Homebrew, OrbStack, Docker Desktop, Rancher Desktop, Colima (Homebrew),
// /usr/local/bin and the Windows Docker Desktop directory.
func CommonDirs(goos, home string) []string {
	switch goos {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		dirs := []string{filepath.Join(pf, "Docker", "Docker", "resources", "bin"), filepath.Join(pf, "Rancher Desktop", "resources", "resources", "win32", "bin")}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".rd", "bin"))
		}
		return dirs
	case "darwin":
		dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".orbstack", "bin"), filepath.Join(home, ".docker", "bin"), filepath.Join(home, ".rd", "bin"))
		}
		return append(dirs, "/Applications/OrbStack.app/Contents/MacOS/xbin", "/Applications/Docker.app/Contents/Resources/bin")
	default:
		dirs := []string{"/usr/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin", "/snap/bin"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".docker", "bin"), filepath.Join(home, ".rd", "bin"), filepath.Join(home, "bin"))
		}
		return dirs
	}
}

// Find locates the docker binary: the configured one, else the process
// PATH, else the login shell PATH, else the common directories.
func Find(ctx context.Context, o FindOptions) (Binary, error) {
	if c := strings.TrimSpace(o.Configured); c != "" {
		if filepath.IsAbs(c) {
			if _, err := shellenv.LookPath(filepath.Base(c), filepath.Dir(c)); err != nil {
				return Binary{}, fmt.Errorf("%w: docker.path %s is not executable", ErrNotFound, c)
			}
			return Binary{Path: c, Source: SourceSetting}, nil
		}
		o.CommonDirs = []string{} // a name: search PATHs only
		b, err := find(ctx, c, o)
		if err != nil {
			return Binary{}, err
		}
		b.Source = SourceSetting
		return b, nil
	}
	return find(ctx, "docker", o)
}

func find(ctx context.Context, name string, o FindOptions) (Binary, error) {
	proc := o.ProcessPath
	if proc == "" {
		proc = os.Getenv("PATH")
	}
	if p, err := shellenv.LookPath(name, proc); err == nil {
		return Binary{Path: p, Source: SourcePath}, nil
	}
	if o.LoginPath != nil {
		if login, err := o.LoginPath(ctx); err == nil && login != "" {
			if p, err := shellenv.LookPath(name, login); err == nil {
				return Binary{Path: p, Source: SourceLoginShell}, nil
			}
		}
	}
	dirs := o.CommonDirs
	if dirs == nil {
		home, _ := os.UserHomeDir()
		dirs = CommonDirs(runtime.GOOS, home)
	}
	if p, err := shellenv.LookPath(name, strings.Join(dirs, string(os.PathListSeparator))); err == nil {
		return Binary{Path: p, Source: SourceCommon}, nil
	}
	return Binary{}, fmt.Errorf("%w: %s not in PATH, login shell PATH or common directories (set docker.path)", ErrNotFound, name)
}

// EnvPath returns the PATH entry ("PATH=…") for running binary: its
// directory first, then the given PATH lists (e.g. process and login
// shell PATH), without duplicates. docker finds its compose plugin and
// credential helpers (docker-credential-osxkeychain) through it.
func EnvPath(binary string, lists ...string) string {
	all := make([]string, 0, len(lists)+1)
	if binary != "" && filepath.IsAbs(binary) {
		all = append(all, filepath.Dir(binary))
	}
	all = append(all, lists...)
	return pathKey() + "=" + shellenv.Merge(all...)
}

func pathKey() string {
	if runtime.GOOS == "windows" {
		return "Path"
	}
	return "PATH"
}

func envKey(k string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(k)
	}
	return k
}
