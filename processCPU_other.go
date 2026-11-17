//go:build !windows

// Santosh Tripathee
// Reads how much CPU time FileSync has used so far (Mac and Linux version).
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
