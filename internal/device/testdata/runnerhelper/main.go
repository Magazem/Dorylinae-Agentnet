// Command runnerhelper is a test program for the own-device helper runner
// (internal/device, internal/daemon tests). Tests build it with go build into
// a temporary directory; it is never part of a release.
//
//	runnerhelper env            print the environment, one KEY=VALUE per line
//	runnerhelper pwd            print the working directory
//	runnerhelper exit N         print a line, exit with code N
//	runnerhelper spam N         print N lines with ANSI colours, CRLF endings and control characters
//	runnerhelper tree FILE      start "beat FILE" as a child, then sleep
//	runnerhelper orphan FILE    start "beat FILE" holding stdout, then exit 0
//	runnerhelper beat FILE      append to FILE every 50 ms, forever
//	runnerhelper sleep S        sleep S seconds
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "env":
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
	case "exit":
		n, _ := strconv.Atoi(os.Args[2])
		fmt.Printf("exiting with %d\n", n)
		os.Exit(n)
	case "spam":
		n, _ := strconv.Atoi(os.Args[2])
		for i := 0; i < n; i++ {
			fmt.Printf("\x1b[31mline\x1b[0m %06d \x1b[1;32mgreen\x1b[m bell\a\r\n", i)
		}
		fmt.Fprint(os.Stderr, "last line on stderr\n")
	case "tree":
		exe, _ := os.Executable()
		child := exec.Command(exe, "beat", os.Args[2])
		if err := child.Start(); err != nil {
			fmt.Println("start:", err)
			os.Exit(1)
		}
		time.Sleep(10 * time.Minute)
	case "orphan":
		// The child inherits stdout and stderr, so it holds the output
		// pipe open after this process exits 0.
		exe, _ := os.Executable()
		child := exec.Command(exe, "beat", os.Args[2])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Println("start:", err)
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	case "beat":
		for {
			f, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = f.WriteString("x")
				_ = f.Close()
			}
			time.Sleep(50 * time.Millisecond)
		}
	case "sleep":
		s, _ := strconv.Atoi(os.Args[2])
		time.Sleep(time.Duration(s) * time.Second)
	default:
		os.Exit(2)
	}
}
