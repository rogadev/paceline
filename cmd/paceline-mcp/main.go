// Command paceline-mcp is an MCP server that lets agents report a long job's
// progress, which the paceline status line draws as a bar.
//
// Claude Code starts it over stdio. Its only effect is writing the progress
// file in a repository's git directory: it starts no processes and never
// touches the network.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/rogadev/paceline/internal/mcp"
)

// version is set at release time with -ldflags "-X main.version=1.2.3".
var version = ""

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, defaultToolset()))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, t toolset) int {
	if len(args) > 0 {
		if args[0] == "--version" || args[0] == "-v" {
			fmt.Fprintln(stdout, buildVersion())
			return 0
		}
		fmt.Fprintln(stderr, errNoArgs)
		return 2
	}
	s := &mcp.Server{Name: "paceline", Version: buildVersion(), Instructions: instructions, Tools: t.tools()}
	if err := s.Serve(stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "paceline-mcp:", err)
		return 1
	}
	return 0
}
