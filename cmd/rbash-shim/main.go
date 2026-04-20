// rbash-shim is the host-side stdio↔unix-socket bridge invoked by
// Claude Code as the MCP command. It resolves a unix socket path
// (CLI arg → $RBASH_SOCKET → $XDG_RUNTIME_DIR/rbash.sock → /run/rbash.sock),
// dials the socket, and copies bytes in both directions until either
// side closes. No MCP parsing — the daemon on the other end handles
// the JSON-RPC protocol.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

func main() {
	socketFlag := flag.String("socket", "", "Unix socket path (overrides $RBASH_SOCKET and defaults)")
	flag.Parse()

	// Positional arg takes priority over --socket, matches the
	// documented "rbash-shim /path/to/sock" form for terse MCP configs.
	arg := *socketFlag
	if flag.NArg() > 0 {
		arg = flag.Arg(0)
	}
	socketPath := resolveSocketPath(arg)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rbash-shim: dial %s: %v\n", socketPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(conn, os.Stdin)
		// EOF on stdin — half-close the write side so the daemon sees it.
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, conn)
	}()

	wg.Wait()
}

func resolveSocketPath(arg string) string {
	if arg != "" {
		return arg
	}
	if v := os.Getenv("RBASH_SOCKET"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "rbash.sock")
	}
	return "/run/rbash.sock"
}
