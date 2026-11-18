package terminal

import (
	"context"
	"strconv"
	"strings"
)

// Shells inside containers (or on other hosts) run on the far side of a
// client program such as `docker exec -it`. Killing that client does not
// end them, so a session can stop them remotely (Options.StopRemote): the
// remote side is started through ReportPIDArgs, which reports the PID of
// the shell in an operating system command the emulator hides, and on
// Close the provider runs RemoteStopArgs(pid) on the remote side.

// pidOSC is the private OSC number of the PID report.
const pidOSC = "7716"

// pidPrefix starts the text of the PID report.
const pidPrefix = "tiegel-pid="

// reportScript (`sh -c reportScript sh PROGRAM ARGS…`) reports its PID
// and execs the program, which keeps that PID. With a TTY the shell is
// the leader of its own session (docker exec -it, ssh -t); jobs it
// starts stay in that session.
const reportScript = `printf '\033]` + pidOSC + `;` + pidPrefix + `%s\007' "$$"; exec "$@"`

// remoteStopScript (`sh -c remoteStopScript sh PID`) ends every process
// of the session (and process group) PID: SIGHUP like a closed terminal
// (shells save their history and hang up their jobs), up to 2 s grace,
// then SIGKILL. Without /proc it signals the group and the process. It
// uses shell builtins only (Debian images have no kill program).
const remoteStopScript = `P=$1; ` +
	`k() { S=$1; n=1; ` +
	`if [ -r /proc/1/stat ]; then for f in /proc/[0-9]*/stat; do ` +
	`read -r l 2>/dev/null <"$f" || continue; l=${l##*") "}; set -- $l; ` +
	`[ "$1" = Z ] && continue; ` +
	`if [ "$4" = "$P" ] || [ "$3" = "$P" ]; then q=${f#/proc/}; kill -"$S" "${q%/stat}" 2>/dev/null && n=0; fi; done; ` +
	`else kill -"$S" -"$P" 2>/dev/null && n=0; kill -"$S" "$P" 2>/dev/null && n=0; fi; return $n; }; ` +
	`k HUP || exit 0; i=0; while k 0 && [ "$i" -lt 10 ]; do sleep 0.2; i=$((i+1)); done; k KILL; exit 0`

// ReportPIDArgs returns the command line (to run on the remote side,
// behind e.g. `docker exec -it CONTAINER`) that reports its PID to the
// session and then runs program with args. It needs a POSIX sh there.
func ReportPIDArgs(program string, args ...string) []string {
	return append([]string{"sh", "-c", reportScript, "sh", program}, args...)
}

// RemoteStopArgs returns the command line (to run on the remote side,
// e.g. `docker exec CONTAINER …` without a TTY) that ends the session of
// the reported PID and everything in it.
func RemoteStopArgs(pid int) []string {
	return []string{"sh", "-c", remoteStopScript, "sh", strconv.Itoa(pid)}
}

// StopRemoteFunc ends the remote processes of a session; pid is the PID
// the program reported through ReportPIDArgs (0: none arrived).
type StopRemoteFunc func(ctx context.Context, pid int) error

// parsePIDReport returns the PID of an OSC pidOSC text (0 if invalid).
func parsePIDReport(text string) int {
	v, ok := strings.CutPrefix(text, pidPrefix)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 1 {
		return 0
	}
	return n
}
