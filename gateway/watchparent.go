package main

import (
	"os"
	"strconv"
	"syscall"
	"time"
)

// watchParent terminates this sidecar when its parent (the Tauri app) dies.
// The parent normally reaps children on graceful exit; this watchdog covers the
// SIGKILL/crash case, where no exit handler runs, so a hard-killed app never
// leaves orphaned services holding loopback ports. Enabled when the parent sets
// ORCHESTRA_PARENT_PID; a no-op otherwise (e.g. when run standalone).
func watchParent() {
	pidStr := os.Getenv("ORCHESTRA_PARENT_PID")
	if pidStr == "" {
		return
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 1 {
		return
	}
	go func() {
		for {
			time.Sleep(2 * time.Second)
			// Reparented to init (ppid 1) or the parent no longer exists.
			if os.Getppid() == 1 || syscall.Kill(pid, 0) != nil {
				os.Exit(0)
			}
		}
	}()
}
