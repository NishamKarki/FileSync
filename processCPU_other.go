//go:build !windows

// Santosh Tripathee
// Reads CPU time consumed by the FileSync process on macOS and Linux.
package main

import (
	"syscall"
	"time"
)

func processCPUTime() time.Duration {
	var resourceUsage syscall.Rusage
	if resourceUsageError := syscall.Getrusage(syscall.RUSAGE_SELF, &resourceUsage); resourceUsageError != nil {
		return 0
	}
	return time.Duration(resourceUsage.Utime.Nano() + resourceUsage.Stime.Nano())
}
