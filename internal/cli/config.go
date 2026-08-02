package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/sean-reid/interviews/internal/interview"
)

// cmdConfig reads and writes the per-machine settings. There is one setting
// that matters: where the problems are, since the platform and the content
// live in separate repositories.
func cmdConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return configShow(stdout, stderr)
	}
	switch args[0] {
	case "set":
		return configSet(args[1:], stdout, stderr)
	case "unset":
		return configUnset(args[1:], stdout, stderr)
	case "--help", "-h":
		printUsage("config", stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "interviews config: no verb %q\n", args[0])
		return usageErr("config", stderr)
	}
}

func configShow(stdout, stderr io.Writer) int {
	home, err := interview.Home()
	if err != nil {
		fmt.Fprintf(stderr, "interviews config: %v\n", err)
		return 1
	}
	root, from := interview.ContentRoot()
	fmt.Fprintf(stdout, "content  %s  (%s)\n", root, from)
	fmt.Fprintf(stdout, "file     %s\n", filepath.Join(home, interview.ConfigFile))
	if from == interview.FromFallback {
		fmt.Fprintln(stdout, "\nno content root set. The problems live in their own repository:")
		fmt.Fprintln(stdout, "  interviews config set content <path to that checkout>/content")
	}
	return 0
}

func configSet(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		return usageErr("config", stderr)
	}
	key, value := args[0], args[1]
	if key != "content" {
		fmt.Fprintf(stderr, "interviews config: no setting %q (there is one: content)\n", key)
		return 2
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		fmt.Fprintf(stderr, "interviews config: %v\n", err)
		return 1
	}
	// Refuse a path that holds no problems. A typo here breaks every command
	// afterwards, and the error it produces would name the path rather than
	// this setting.
	if _, err := openRegistry(abs, false, io.Discard); err != nil {
		fmt.Fprintf(stderr, "interviews config: %v\n", err)
		fmt.Fprintf(stderr, "point content at the content directory inside the problems checkout\n")
		return 1
	}
	c := interview.LoadConfig()
	c.ContentRoot = abs
	if err := interview.SaveConfig(c); err != nil {
		fmt.Fprintf(stderr, "interviews config: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "content set to %s\n", abs)
	return 0
}

func configUnset(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "content" {
		return usageErr("config", stderr)
	}
	c := interview.LoadConfig()
	c.ContentRoot = ""
	if err := interview.SaveConfig(c); err != nil {
		fmt.Fprintf(stderr, "interviews config: %v\n", err)
		return 1
	}
	root, from := interview.ContentRoot()
	fmt.Fprintf(stdout, "content unset; now %s (%s)\n", root, from)
	return 0
}
