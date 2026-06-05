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
	case "setup":
		runSetup(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: cockpit <serve|agent|setup|version|upgrade|service> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  setup serve   一鍵設定控制台伺服器（建立設定、安裝服務）")
	fmt.Fprintln(os.Stderr, "  setup agent   一鍵設定監控 agent（建立設定、安裝服務）")
}
