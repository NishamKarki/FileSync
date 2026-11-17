//go:build windows

// Santosh Tripathee
// Reads how much CPU time FileSync has used so far (Windows version).
// We read it before and after a sync; the difference is the CPU used by that sync.
package main

import (
	"syscall"
	"time"
)

func processCPUTime() time.Duration {
	currentProcess, processHandleError := syscall.GetCurrentProcess()
	if processHandleError != nil {
		return 0
	}
	var creationTime, exitTime, kernelTime, userTime syscall.Filetime
	if processTimesError := syscall.GetProcessTimes(currentProcess, &creationTime, &exitTime, &kernelTime, &userTime); processTimesError != nil {
		return 0
	}
	// Windows counts CPU time in steps of 100 nanoseconds
	totalSteps := filetimeSteps(kernelTime) + filetimeSteps(userTime)
	return time.Duration(totalSteps * 100)
}

func filetimeSteps(fileTime syscall.Filetime) int64 {
	return int64(fileTime.HighDateTime)<<32 | int64(fileTime.LowDateTime)
}
