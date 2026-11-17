//go:build linux

// Command tetragon-harness stands in for the service in the Tetragon
// runtime-security E2E test (e2e/runtime-security.sh). It runs as /server in
// a pod that the policies in k8s/runtime-security/ select, so it gets the
// same treatment as the real service.
//
// It listens on :8080 (so the pod passes its tcpSocket probes), waits for
// Tetragon to pick up the new container, performs the single action named by
// its arguments, and then prints "survived" and keeps running. A process
// killed by a policy never prints "survived" and its container exits with
// code 137 (SIGKILL).
//
//	idle                 do nothing
//	exec <path>          execve(path)
//	listen <port>        listen on another TCP port
//	connect <host:port>  open an outbound TCP connection
//	ptrace               call ptrace(PTRACE_TRACEME)
package main

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// startDelay gives Tetragon time to associate the new container with its pod
// before the action runs.
const startDelay = 10 * time.Second

func main() {
	if len(os.Args) < 2 {
		fail("usage: server idle|exec <path>|listen <port>|connect <host:port>|ptrace")
	}

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		fail("listen :8080: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	time.Sleep(startDelay)
	fmt.Println("running action:", os.Args[1:])

	switch os.Args[1] {
	case "idle":
	case "exec":
		need(3)
		err := syscall.Exec(os.Args[2], []string{os.Args[2], "idle"}, nil)
		fmt.Println("exec returned:", err)
	case "listen":
		need(3)
		_, err := net.Listen("tcp", ":"+os.Args[2])
		fmt.Println("listen returned:", err)
	case "connect":
		need(3)
		c, err := net.DialTimeout("tcp", os.Args[2], 3*time.Second)
		if err == nil {
			_ = c.Close()
		}
		fmt.Println("connect returned:", err)
	case "ptrace":
		_, _, errno := syscall.RawSyscall(syscall.SYS_PTRACE, syscall.PTRACE_TRACEME, 0, 0)
		fmt.Println("ptrace returned errno:", errno)
	default:
		fail("unknown action %q", os.Args[1])
	}

	fmt.Println("survived")
	select {}
}

func need(n int) {
	if len(os.Args) < n {
		fail("action %q needs an argument", os.Args[1])
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
