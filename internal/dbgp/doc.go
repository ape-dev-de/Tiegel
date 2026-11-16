// Package dbgp implements the IDE side of the DBGp debugger protocol for
// Xdebug 3.
//
// A [Server] listens for incoming Xdebug connections (default
// 127.0.0.1:9003). Every connection becomes a [Session] that offers a
// high-level API (Run, StepOver, Stack, Variables, Eval, SetBreakpoint, ...)
// on top of the raw protocol. Lifecycle and status changes are reported via
// the callbacks in [Events].
//
// File names exchanged with the debugger engine are file:// URIs that refer
// to the engine's (remote) file system. A [PathMapper] translates them to host
// paths and back, e.g. for PHP running inside a Docker container.
//
// The package does not depend on any UI toolkit.
package dbgp
