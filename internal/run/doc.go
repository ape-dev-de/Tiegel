// Package run is Tiegel's run infrastructure (AP15.1): it runs processes
// on the host or inside the project's PHP interpreter (local binary,
// Docker Compose service via exec or run, Docker image, WSL), streams
// their output line by line, maps file locations in the output back to
// host files, stops whole process trees and keeps the runs for rerun.
//
//   - Spec says what to run (php with arguments or a program, working
//     directory, environment, interpreter); Prepare turns it into a
//     Launch for a Target (interpreter plus path mappings).
//   - Manager starts launches, numbers their output lines, batches them
//     into events, keeps a bounded tail per run and stops runs (process
//     tree, plus the process inside a container where killing the docker
//     client is not enough).
//   - Linker finds "path:line", "path(line)" and "in path on line N" in
//     output and maps container paths to host files.
//   - Config and ConfigType are run configurations: the kernel's core
//     types (PHP script, command, external tool with macros) and types contributed
//     by plugins through a hook, with fields declared like settings specs.
//
// The package imports no Gio and no API code; internal/api wires it to
// the hooks (interpreter resolution, run.before guard, config types) and
// to the bus.
package run
