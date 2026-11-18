// Package dockertest provides a portable fake docker CLI for tests: the
// test binary itself plays docker when started with the environment
// variable EnvDir set. A test package calls Main at the start of its
// TestMain and passes Fake's binary and environment to the code under
// test. It works on Windows, Linux and macOS without a shell.
//
// The fake answers:
//
//	context ls --format json   contents of <dir>/contexts.json
//	version --format json      <dir>/version.json; contexts listed in
//	                           <dir>/down (one per line) fail like an
//	                           unreachable engine; <dir>/slow sleeps 5 s
//	image ls -q REF            an ID if REF is listed in <dir>/images
//	compose … ps …             contents of <dir>/ps.json
//	compose … logs …, logs …   contents of <dir>/logs.txt; with --follow
//	                           it then prints <dir>/logs-next (removing
//	                           it) whenever it appears, until killed
//	compose … up|down|start|stop|restart …, start|stop|restart ID
//	                           print progress on stderr; fail when the
//	                           action is listed in <dir>/fail
//	ps …                       contents of <dir>/containers.json
//	events …                   contents of <dir>/events.json, then
//	                           <dir>/events-next whenever it appears,
//	                           until killed; fails when <dir>/events-fail
//	                           exists
//	compose … exec … sh -c …, exec … sh -c …
//	                           succeed without output (stop helpers)
//	compose … exec|run …, run …  behave like php (-m, -r with the version,
//	                           ini or probe script); `run` of an image not
//	                           in <dir>/images with --pull never fails
//
// Every invocation is appended to <dir>/args.log (one line, args joined
// by spaces).
package dockertest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnvDir names the fake's state directory; when set, Main plays docker.
const EnvDir = "TIEGEL_FAKE_DOCKER_DIR"

// Main plays docker and exits when EnvDir is set; otherwise it returns.
// Call it first in TestMain.
func Main() {
	dir := os.Getenv(EnvDir)
	if dir == "" {
		return
	}
	os.Exit(play(dir, os.Args[1:]))
}

// Fake is a fake docker installation for one test.
type Fake struct {
	// Dir holds the fake's files (contexts.json, ps.json, …).
	Dir string
	// Binary is the executable to use as docker (the test binary).
	Binary string
	// Env must be added to the environment of every docker run.
	Env []string
}

// Contexts in the format of `docker context ls --format json`: OrbStack
// (current), Docker Desktop and default.
const Contexts = `{"Current":false,"Description":"Current DOCKER_HOST based configuration","DockerEndpoint":"unix:///var/run/docker.sock","Error":"","Name":"default"}
{"Current":false,"Description":"Docker Desktop","DockerEndpoint":"unix:///Users/me/.docker/run/docker.sock","Error":"","Name":"desktop-linux"}
{"Current":true,"Description":"OrbStack","DockerEndpoint":"unix:///Users/me/.orbstack/run/docker.sock","Error":"","Name":"orbstack"}
`

// Version in the format of `docker version --format json` (OrbStack).
const Version = `{"Client":{"Version":"27.3.1","ApiVersion":"1.47","Os":"darwin","Arch":"arm64","Context":"orbstack"},"Server":{"Platform":{"Name":"Docker Engine - Community"},"Version":"27.3.1","ApiVersion":"1.47","Os":"linux","Arch":"arm64","KernelVersion":"6.10.14-orbstack-00280-g1304bd068592"}}`

// TB is the subset of testing.TB that New needs.
type TB interface {
	Helper()
	TempDir() string
	Fatal(args ...any)
}

// New creates a fake with the default contexts and version, no images and
// no compose containers.
func New(t TB) *Fake {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{Dir: t.TempDir(), Binary: exe}
	f.Env = []string{EnvDir + "=" + f.Dir}
	f.Write(t, "contexts.json", Contexts)
	f.Write(t, "version.json", Version)
	return f
}

// Write sets a file of the fake.
func (f *Fake) Write(t TB, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.Dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Emit publishes content as file name atomically (write and rename), for
// the files a running fake picks up (logs-next, events-next). It waits
// until a previous emission was consumed.
func (f *Fake) Emit(t TB, name, content string) {
	t.Helper()
	p := filepath.Join(f.Dir, name)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(p); err != nil {
			break
		}
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
}

// Remove deletes a file of the fake (no error if missing).
func (f *Fake) Remove(name string) { _ = os.Remove(filepath.Join(f.Dir, name)) }

// Calls returns the logged invocations.
func (f *Fake) Calls() []string {
	data, err := os.ReadFile(filepath.Join(f.Dir, "args.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// Called reports whether an invocation containing all parts was logged.
func (f *Fake) Called(parts ...string) bool {
	for _, c := range f.Calls() {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(c, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func read(dir, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	return string(data), err == nil
}

func listed(dir, name, value string) bool {
	data, _ := read(dir, name)
	for _, l := range strings.Split(data, "\n") {
		if strings.TrimSpace(l) == value && value != "" {
			return true
		}
	}
	return false
}

func play(dir string, args []string) int {
	if f, err := os.OpenFile(filepath.Join(dir, "args.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		_ = f.Close()
	}
	ctxName := ""
	if len(args) >= 2 && args[0] == "--context" {
		ctxName, args = args[1], args[2:]
	}
	if len(args) == 0 {
		return fail("no command")
	}
	switch args[0] {
	case "context":
		out, _ := read(dir, "contexts.json")
		fmt.Print(out)
		return 0
	case "version":
		if _, slow := read(dir, "slow"); slow {
			time.Sleep(5 * time.Second)
		}
		if ctxName == "" {
			ctxName = "orbstack"
		}
		if listed(dir, "down", ctxName) {
			fmt.Printf(`{"Client":{"Version":"27.3.1","Context":%q},"Server":null}`+"\n", ctxName)
			return fail("Cannot connect to the Docker daemon at unix:///nowhere. Is the docker daemon running?")
		}
		out, _ := read(dir, "version.json")
		fmt.Println(out)
		return 0
	case "image":
		if listed(dir, "images", args[len(args)-1]) {
			fmt.Println("sha256:0123456789abcdef")
		}
		return 0
	case "compose":
		switch sub := composeSub(args); sub {
		case "ps":
			out, _ := read(dir, "ps.json")
			fmt.Print(out)
			return 0
		case "logs":
			return logs(dir, args[subIndex(args)+1:])
		case "up", "down", "start", "stop", "restart":
			return action(dir, sub)
		}
		if shellHelper(args) {
			return 0
		}
		return php(dir, args)
	case "exec":
		if shellHelper(args) {
			return 0
		}
		return fail("exec: only sh -c helpers are played")
	case "start", "stop", "restart":
		return action(dir, args[0])
	case "logs":
		return logs(dir, args[1:])
	case "ps":
		out, _ := read(dir, "containers.json")
		fmt.Print(out)
		return 0
	case "events":
		if _, bad := read(dir, "events-fail"); bad {
			return fail("Error response from daemon: events not supported")
		}
		out, _ := read(dir, "events.json")
		fmt.Print(out)
		tail(dir, "events-next")
		return 0
	case "run":
		noPull := false
		for i, a := range args {
			if a == "--pull" && i+1 < len(args) && args[i+1] == "never" {
				noPull = true
			}
			if a == "--entrypoint" && i+2 < len(args) {
				image := args[i+2]
				if noPull && !listed(dir, "images", image) {
					return fail("Unable to find image '" + image + "' locally")
				}
				return php(dir, args[i+3:])
			}
		}
		return fail("run without --entrypoint")
	}
	return fail("unknown command " + args[0])
}

// shellHelper reports whether args run `sh -c SCRIPT …` (stop helpers,
// which the fake answers with success).
func shellHelper(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "sh" && args[i+1] == "-c" {
			return true
		}
	}
	return false
}

// subIndex returns the index of the compose subcommand after the global
// options (len(args) if there is none).
func subIndex(args []string) int {
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--project-directory", "-p", "-f", "--profile":
			i++
			continue
		}
		return i
	}
	return len(args)
}

// composeSub returns the compose subcommand after the global options.
func composeSub(args []string) string {
	if i := subIndex(args); i < len(args) {
		return args[i]
	}
	return ""
}

// logs prints logs.txt and, with --follow, keeps printing logs-next.
func logs(dir string, args []string) int {
	out, _ := read(dir, "logs.txt")
	fmt.Print(out)
	for _, a := range args {
		if a == "--follow" || a == "-f" {
			tail(dir, "logs-next")
		}
	}
	return 0
}

// action plays a compose or container action.
func action(dir, name string) int {
	if listed(dir, "fail", name) {
		return fail("Error response from daemon: " + name + " failed")
	}
	fmt.Fprintf(os.Stderr, " Container fake-1  %s\n", name)
	return 0
}

// tail prints the file name whenever it appears (and removes it) until
// the process is killed; it gives up after a minute.
func tail(dir, name string) {
	p := filepath.Join(dir, name)
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		if data, err := os.ReadFile(p); err == nil {
			_ = os.Remove(p)
			fmt.Print(string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// php answers the queries of package interp.
func php(dir string, args []string) int {
	version := "8.3.1"
	if v, ok := read(dir, "php-version"); ok {
		version = strings.TrimSpace(v)
	}
	for i, a := range args {
		switch a {
		case "-m":
			fmt.Print("[PHP Modules]\nCore\njson\nxdebug\n\n[Zend Modules]\nXdebug\n")
			return 0
		case "-r":
			if i+1 >= len(args) {
				return fail("-r without code")
			}
			code := args[i+1]
			switch {
			case strings.Contains(code, "__TIEGEL_PROBE__"):
				fmt.Printf("__TIEGEL_PROBE__{\"version\":%q,\"sapi\":\"cli\",\"os\":\"Linux\",\"binary\":\"/usr/local/bin/php\","+
					"\"extensions\":[\"json\",\"Core\",\"xdebug\"],\"zendExtensions\":[\"Xdebug\"],\"xdebug\":\"3.3.2\","+
					"\"iniFile\":\"\",\"iniScanned\":[\"/usr/local/etc/php/conf.d/docker-php-ext-xdebug.ini\"],\"ini\":{\"xdebug.mode\":\"debug\"}}\n", version)
			case strings.Contains(code, "ini_get"):
				fmt.Print("xdebug.mode=debug\nxdebug.client_host=host.docker.internal\nxdebug.client_port=9003\n")
			case strings.Contains(code, "PHP_VERSION"):
				fmt.Print(version)
			default:
				return fail("unknown php code")
			}
			return 0
		}
	}
	return fail("bad php args: " + strings.Join(args, " "))
}

func fail(msg string) int {
	fmt.Fprintln(os.Stderr, msg)
	return 1
}
