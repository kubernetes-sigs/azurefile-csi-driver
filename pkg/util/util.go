/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	GiB                  = 1024 * 1024 * 1024
	MaxPathLengthWindows = 260
)

type AzcopyJobState string

const (
	AzcopyJobError     AzcopyJobState = "Error"
	AzcopyJobNotFound  AzcopyJobState = "NotFound"
	AzcopyJobRunning   AzcopyJobState = "Running"
	AzcopyJobCompleted AzcopyJobState = "Completed"
)

var mutex = &sync.Mutex{}

// RoundUpBytes rounds up the volume size in bytes up to multiplications of GiB
// in the unit of Bytes
func RoundUpBytes(volumeSizeBytes int64) int64 {
	return roundUpSize(volumeSizeBytes, GiB) * GiB
}

// RoundUpGiB rounds up the volume size in bytes up to multiplications of GiB
// in the unit of GiB
func RoundUpGiB(volumeSizeBytes int64) int64 {
	return roundUpSize(volumeSizeBytes, GiB)
}

// BytesToGiB conversts Bytes to GiB
func BytesToGiB(volumeSizeBytes int64) int64 {
	return volumeSizeBytes / GiB
}

// GiBToBytes converts GiB to Bytes
func GiBToBytes(volumeSizeGiB int64) int64 {
	return volumeSizeGiB * GiB
}

// roundUpSize calculates how many allocation units are needed to accommodate
// a volume of given size. E.g. when user wants 1500MiB volume, while Azure File
// allocates volumes in gibibyte-sized chunks,
// RoundUpSize(1500 * 1024*1024, 1024*1024*1024) returns '2'
// (2 GiB is the smallest allocatable volume that can hold 1500MiB)
func roundUpSize(volumeSizeBytes int64, allocationUnitBytes int64) int64 {
	roundedUp := volumeSizeBytes / allocationUnitBytes
	if volumeSizeBytes%allocationUnitBytes > 0 {
		roundedUp++
	}
	return roundedUp
}

func RunPowershellCmd(command string, envs ...string) ([]byte, error) {
	// only one powershell command can be executed at a time to avoid OOM
	mutex.Lock()
	defer mutex.Unlock()

	cmd := exec.Command("powershell", "-Mta", "-NoProfile", "-Command", command)
	cmd.Env = append(os.Environ(), envs...)
	klog.V(8).Infof("Executing command: %q", cmd.String())
	return cmd.CombinedOutput()
}

type EXEC interface {
	RunCommand(string, []string) (string, error)
}

type ExecCommand struct {
}

func (ec *ExecCommand) RunCommand(cmdStr string, authEnv []string) (string, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	if len(authEnv) > 0 {
		cmd.Env = append(os.Environ(), authEnv...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type Azcopy struct {
	ExecCmd EXEC
}

// GetAzcopyJob get the azcopy job status if job existed
func (ac *Azcopy) GetAzcopyJob(dstFileshare string, authAzcopyEnv []string) (AzcopyJobState, string, error) {
	cmdStr := fmt.Sprintf("azcopy jobs list | grep %s -B 3", dstFileshare)
	// cmd output example:
	// JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9
	// Start Time: Monday, 07-Aug-23 03:29:54 UTC
	// Status: Completed (or Cancelled, InProgress)
	// Command: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false
	// --
	// JobId: b598cce3-9aa9-9640-7793-c2bf3c385a9a
	// Start Time: Wednesday, 09-Aug-23 09:09:03 UTC
	// Status: Cancelled
	// Command: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false
	if ac.ExecCmd == nil {
		ac.ExecCmd = &ExecCommand{}
	}
	out, err := ac.ExecCmd.RunCommand(cmdStr, authAzcopyEnv)
	// if grep command returns nothing, the exec will return exit status 1 error, so filter this error
	if err != nil && err.Error() != "exit status 1" {
		klog.Warningf("failed to get azcopy job with error: %v, jobState: %v", err, AzcopyJobError)
		return AzcopyJobError, "", fmt.Errorf("couldn't list jobs in azcopy %v", err)
	}
	jobid, jobState, err := parseAzcopyJobList(out)
	if err != nil || jobState == AzcopyJobError {
		klog.Warningf("failed to get azcopy job with error: %v, jobState: %v", err, jobState)
		return AzcopyJobError, "", fmt.Errorf("couldn't parse azcopy job list in azcopy %v", err)
	}
	if jobState == AzcopyJobCompleted {
		return jobState, "100.0", err
	}
	if jobid == "" {
		return jobState, "", err
	}
	cmdPercentStr := fmt.Sprintf("azcopy jobs show %s | grep Percent", jobid)
	// cmd out example:
	// Percent Complete (approx): 100.0
	summary, err := ac.ExecCmd.RunCommand(cmdPercentStr, authAzcopyEnv)
	if err != nil {
		klog.Warningf("failed to get azcopy job with error: %v, jobState: %v", err, AzcopyJobError)
		return AzcopyJobError, "", fmt.Errorf("couldn't show jobs summary in azcopy %v", err)
	}
	jobState, percent, err := parseAzcopyJobShow(summary)
	if err != nil || jobState == AzcopyJobError {
		klog.Warningf("failed to get azcopy job with error: %v, jobState: %v", err, jobState)
		return AzcopyJobError, "", fmt.Errorf("couldn't parse azcopy job show in azcopy %v", err)
	}
	return jobState, percent, nil
}

// TestListJobs test azcopy jobs list command with authAzcopyEnv
func (ac *Azcopy) TestListJobs(accountName, storageEndpointSuffix string, authAzcopyEnv []string) (string, error) {
	cmdStr := fmt.Sprintf("azcopy list %s", fmt.Sprintf("https://%s.file.%s", accountName, storageEndpointSuffix))
	if ac.ExecCmd == nil {
		ac.ExecCmd = &ExecCommand{}
	}
	return ac.ExecCmd.RunCommand(cmdStr, authAzcopyEnv)
}

// parseAzcopyJobList parse command azcopy jobs list, get jobid and state from joblist
func parseAzcopyJobList(joblist string) (string, AzcopyJobState, error) {
	jobid := ""
	jobSegments := strings.Split(joblist, "JobId: ")
	if len(jobSegments) < 2 {
		return jobid, AzcopyJobNotFound, nil
	}
	jobSegments = jobSegments[1:]
	for _, job := range jobSegments {
		segments := strings.Split(job, "\n")
		if len(segments) < 4 {
			return jobid, AzcopyJobError, fmt.Errorf("error parsing jobs list: %s", job)
		}
		statusSegments := strings.Split(segments[2], ": ")
		if len(statusSegments) < 2 {
			return jobid, AzcopyJobError, fmt.Errorf("error parsing jobs list status: %s", segments[2])
		}
		status := statusSegments[1]
		switch status {
		case "InProgress":
			jobid = segments[0]
		case "Completed":
			return jobid, AzcopyJobCompleted, nil
		}
	}
	if jobid == "" {
		return jobid, AzcopyJobNotFound, nil
	}
	return jobid, AzcopyJobRunning, nil
}

// parseAzcopyJobShow parse command azcopy jobs show jobid, get job state and copy percent
func parseAzcopyJobShow(jobshow string) (AzcopyJobState, string, error) {
	segments := strings.Split(jobshow, ": ")
	if len(segments) < 2 {
		return AzcopyJobError, "", fmt.Errorf("error parsing jobs summary: %s in Percent Complete (approx)", jobshow)
	}
	return AzcopyJobRunning, strings.ReplaceAll(segments[1], "\n", ""), nil
}

// ExecFunc returns a exec function's output and error
type ExecFunc func() (err error)

// TimeoutFunc returns output and error if an ExecFunc timeout
type TimeoutFunc func() (err error)

// WaitUntilTimeout waits for the exec function to complete or return timeout error
func WaitUntilTimeout(timeout time.Duration, execFunc ExecFunc, timeoutFunc TimeoutFunc) error {
	// Create a channel to receive the result of the azcopy exec function
	done := make(chan bool)
	var err error

	// Start the azcopy exec function in a goroutine
	go func() {
		err = execFunc()
		done <- true
	}()

	// Wait for the function to complete or time out
	select {
	case <-done:
		return err
	case <-time.After(timeout):
		return timeoutFunc()
	}
}
