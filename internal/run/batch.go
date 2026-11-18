package run

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

// ErrBatchArgument marks an argument WindowsBatchSafe refuses to pass to
// a batch file.
var ErrBatchArgument = errors.New("unsafe argument for the batch file")

// BatchCommand is a command made safe for Windows batch files: Program
// with Args, or with PHP the php of the target running Args (the first
// being the script).
type BatchCommand struct {
	Program string
	Args    []string
	PHP     bool
}

// BatchFS is what WindowsBatchSafe needs from the host: PATH lookup and
// file inspection (fakes in tests).
type BatchFS struct {
	// Look finds a bare program name in PATH.
	Look func(name string) (string, error)
	// IsFile reports whether path is a regular file (nil: os.Stat).
	IsFile func(path string) bool
	// IsPHP reports whether the file is a PHP script (nil: by content).
	IsPHP func(path string) bool
}

// WindowsBatchSafe returns the command running program with args in dir
// directly on the host of system goos (runtime.GOOS; a parameter for
// tests).
//
// On Windows, processes started on the host must not run batch files
// with arguments from the repository or the editor: cmd.exe interprets
// &, |, % and others in them, and Go only quotes for CommandLineToArgvW
// (BatBadBut). Composer's vendor/bin/<tool>.bat only runs
// `php vendor/bin/<tool>`, so the PHP script is run with php directly
// (as test runs do, D315); a batch file without such a script gets no
// argument with cmd.exe metacharacters (D362, D461). Elsewhere program
// runs unchanged. Relative program paths with a separator are relative
// to dir.
func WindowsBatchSafe(goos, dir, program string, args []string, fsys BatchFS) (BatchCommand, error) {
	plain := BatchCommand{Program: program, Args: args}
	if goos != "windows" {
		return plain, nil
	}
	if fsys.IsFile == nil {
		fsys.IsFile = isRegularFile
	}
	if fsys.IsPHP == nil {
		fsys.IsPHP = IsPHPScript
	}
	php := func(script string) BatchCommand {
		return BatchCommand{PHP: true, Args: append([]string{script}, args...)}
	}
	// target is the file Windows would execute.
	var target string
	if !strings.ContainsAny(program, `/\`) {
		if fsys.Look == nil {
			return plain, nil
		}
		found, err := fsys.Look(program)
		if err != nil {
			return plain, nil // reported by the run infrastructure
		}
		target = found
	} else {
		abs := program
		if !IsAbsAny(program) {
			abs = filepath.Join(dir, filepath.FromSlash(program))
		}
		switch ext := strings.ToLower(ExtAny(abs)); {
		case ext == ".php" || ext == ".phar":
			return php(program), nil
		case ext == "" && fsys.IsFile(abs) && fsys.IsPHP(abs):
			return php(program), nil
		case ext == "":
			// Resolved by the PATHEXT rules: program.exe, .bat, .cmd.
			target = abs
			for _, e := range []string{".com", ".exe", ".bat", ".cmd"} {
				if fsys.IsFile(abs + e) {
					target = abs + e
					break
				}
			}
		default:
			target = abs
		}
	}
	switch ext := strings.ToLower(ExtAny(target)); ext {
	case ".php", ".phar":
		return php(target), nil
	case "":
		if fsys.IsFile(target) && fsys.IsPHP(target) {
			return php(target), nil
		}
	case ".bat", ".cmd":
		// Composer's proxy: the PHP script next to it.
		if script := target[:len(target)-len(ext)]; fsys.IsFile(script) && fsys.IsPHP(script) {
			return php(script), nil
		}
		for _, a := range args {
			if i := strings.IndexFunc(a, CmdUnsafe); i >= 0 {
				return BatchCommand{}, fmt.Errorf("%w %s: refusing to pass %q (cmd.exe would interpret %q); set the tool's path to its PHP script or an .exe", ErrBatchArgument, target, a, a[i])
			}
		}
	}
	return plain, nil
}

// CmdUnsafe reports characters cmd.exe interprets in the command line of
// a batch file even inside Go's quoting.
func CmdUnsafe(r rune) bool {
	return r < 0x20 || strings.ContainsRune(`"%!^&|<>()`, r)
}

// IsAbsAny reports an absolute path in host or Windows syntax (commands
// are built for Windows also in tests on other systems).
func IsAbsAny(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// ExtAny is filepath.Ext for host or Windows separators.
func ExtAny(p string) string {
	base := p[strings.LastIndexAny(p, `/\`)+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[i:]
	}
	return ""
}

// IsPHPScript reports whether the file starts like a PHP script: a php
// shebang or the <?php tag (Composer's proxies in vendor/bin).
func IsPHPScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if bytes.HasPrefix(head, []byte("#!")) {
		line, _, _ := bytes.Cut(head, []byte("\n"))
		return bytes.Contains(line, []byte("php"))
	}
	return bytes.Contains(head, []byte("<?php"))
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// hostSide reports whether t starts processes directly on the host (no
// interpreter or a local php), where Windows command line rules apply.
func hostSide(t Target) bool {
	switch t.Interp.(type) {
	case nil, *interp.Local:
		return true
	}
	return false
}

// batchGuarded reports whether the spec gets the batch file protection of
// WindowsBatchSafe on the host: External Tools, whose arguments carry
// editor and repository content through macros (D461).
func batchGuarded(spec Spec) bool {
	return !spec.PHP && spec.Type == TypeExternalTool
}

// guardBatch applies WindowsBatchSafe to a guarded spec running on a
// host-side target t of system goos; dir is the working directory and
// look the PATH lookup of the launch.
func guardBatch(goos string, spec Spec, t Target, dir string, fsys BatchFS) (Spec, error) {
	if !batchGuarded(spec) || !hostSide(t) {
		return spec, nil
	}
	bc, err := WindowsBatchSafe(goos, dir, spec.Program, spec.Args, fsys)
	if err != nil {
		return Spec{}, fmt.Errorf("run: %w", err)
	}
	if bc.PHP {
		spec.PHP, spec.Program = true, ""
	}
	spec.Args = bc.Args
	return spec, nil
}
