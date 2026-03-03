package main

import (
	"fmt"
	"os"
)

const manifestPathDefault = "bridges.manifest.yml"

var envDomains = map[string]string{
	"prod":    "beeper.com",
	"staging": "beeper-staging.com",
	"dev":     "beeper-dev.com",
	"local":   "beeper.localtest.me",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		printUsage()
		return nil
	}
	switch os.Args[1] {
	case "login":
		return cmdLogin(os.Args[2:])
	case "logout":
		return cmdLogout(os.Args[2:])
	case "whoami":
		return cmdWhoami(os.Args[2:])
	case "up":
		return cmdUp(os.Args[2:])
	case "down":
		return cmdDown(os.Args[2:])
	case "restart":
		return cmdRestart(os.Args[2:])
	case "status":
		return cmdStatus(os.Args[2:])
	case "logs":
		return cmdLogs(os.Args[2:])
	case "init":
		return cmdInit(os.Args[2:])
	case "register":
		return cmdRegister(os.Args[2:])
	case "delete":
		return cmdDelete(os.Args[2:])
	case "list":
		return cmdList(os.Args[2:])
	case "doctor":
		return cmdDoctor(os.Args[2:])
	case "run":
		return cmdRun(os.Args[2:])
	case "auth":
		return cmdAuth(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func printUsage() {
	fmt.Println("bridgectl - bridgev2 orchestrator")
	fmt.Println("commands: login logout whoami register delete up down run restart status logs init list doctor auth help")
}
