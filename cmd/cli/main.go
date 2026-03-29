package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "hook" {
		fmt.Fprintln(os.Stderr, "usage: iclude-cli hook <session-start|capture|session-stop>")
		os.Exit(1)
	}

	var err error
	switch os.Args[2] {
	case "session-start":
		err = runSessionStart()
	case "capture":
		err = runCapture()
	case "session-stop":
		err = runSessionStop()
	default:
		fmt.Fprintf(os.Stderr, "unknown hook subcommand: %s\n", os.Args[2])
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "hook error: %v\n", err)
		os.Exit(1)
	}
}
