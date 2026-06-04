package main

import (
	"fmt"
	"os"
)

var version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "agent":
		runAgent(os.Args[2:])
	case "version":
		fmt.Println("cockpit", version)
	case "upgrade":
		runUpgrade(os.Args[2:])
	case "service":
		runService(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cockpit <serve|agent|version|upgrade|service> [flags]")
}
