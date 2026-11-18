package qakit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// toolCmd is the command of a tool run: Program with Args, or with PHP
// the php of the target running Args (the first being the script).
type toolCmd struct {
	Program string
	Args    []string
	PHP     bool
}

// hostFS is what toolCommand needs from the host: PATH lookup and file
// inspection (fakes in tests).
type hostFS struct {
	// look finds a bare program name in PATH.
	look func(name string) (string, error)
	// isFile reports whether path is a regular file (nil: os.Stat).
	isFile func(path string) bool
	// isPHP reports whether the file is a PHP script (nil: by content).
	isPHP func(path string) bool
}

// toolCommand builds the command running program with args in root.
//
// On Windows, processes started directly on the host (hostSide) must not
// run batch files with arguments from the repository: cmd.exe interprets
// &, |, % and others in them, and Go only quotes for CommandLineToArgvW
// (BatBadBut). Composer's vendor/bin/<tool>.bat only runs
// `php vendor/bin/<tool>`, so the PHP script is run with php directly
// (as test runs do, D315); a batch file without such a script gets no
// argument with cmd.exe metacharacters (D362). Elsewhere program runs
// unchanged.
func toolCommand(goos string, hostSide bool, root, program string, args []string, fsys hostFS) (toolCmd, error) {
	plain := toolCmd{Program: program, Args: args}
	if goos != "windows" || !hostSide {
		return plain, nil
	}
	if fsys.isFile == nil {
		fsys.isFile = isFile
	}
	if fsys.isPHP == nil {
		fsys.isPHP = isPHPScript
	}
	php := func(script string) toolCmd {
		return toolCmd{PHP: true, Args: append([]string{script}, args...)}
	}
	// target is the file Windows would execute.
	var target string
	if !strings.ContainsAny(program, `/\`) {
		found, err := fsys.look(program)
		if err != nil {
			return plain, nil // reported by the run infrastructure
		}
		target = found
	} else {
		abs := program
		if !isAbsAny(program) {
			abs = filepath.Join(root, filepath.FromSlash(program))
		}
		switch ext := strings.ToLower(extAny(abs)); {
		case ext == ".php" || ext == ".phar":
			return php(program), nil
		case ext == "" && fsys.isFile(abs) && fsys.isPHP(abs):
			return php(program), nil
		case ext == "":
			// Resolved by the PATHEXT rules: program.exe, .bat, .cmd.
			target = abs
			for _, e := range []string{".com", ".exe", ".bat", ".cmd"} {
				if fsys.isFile(abs + e) {
					target = abs + e
					break
				}
			}
		default:
			target = abs
		}
	}
	switch ext := strings.ToLower(extAny(target)); ext {
	case ".php", ".phar":
		return php(target), nil
	case "":
		if fsys.isFile(target) && fsys.isPHP(target) {
			return php(target), nil
		}
	case ".bat", ".cmd":
		// Composer's proxy: the PHP script next to it.
		if script := target[:len(target)-len(ext)]; fsys.isFile(script) && fsys.isPHP(script) {
			return php(script), nil
		}
		for _, a := range args {
			if i := strings.IndexFunc(a, cmdUnsafe); i >= 0 {
				return toolCmd{}, fmt.Errorf("%s is a batch file: refusing to pass %q (cmd.exe would interpret %q); set the tool's path to its PHP script or an .exe", target, a, a[i])
			}
		}
	}
	return plain, nil
}

// cmdUnsafe reports characters cmd.exe interprets in the command line of
// a batch file even inside Go's quoting.
func cmdUnsafe(r rune) bool {
	return r < 0x20 || strings.ContainsRune(`"%!^&|<>()`, r)
}

// isAbsAny reports an absolute path in host or Windows syntax (the
// command is built for Windows also in tests on other systems).
func isAbsAny(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// extAny is filepath.Ext for host or Windows separators.
func extAny(p string) string {
	base := p[strings.LastIndexAny(p, `/\`)+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[i:]
	}
	return ""
}

// isPHPScript reports whether the file starts like a PHP script: a php
// shebang or the <?php tag (Composer's proxies in vendor/bin).
func isPHPScript(path string) bool {
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
