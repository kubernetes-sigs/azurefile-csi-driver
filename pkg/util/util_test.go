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
	"reflect"
	"testing"
	"time"

	gomock "go.uber.org/mock/gomock"
)

func TestRoundUpBytes(t *testing.T) {
	var sizeInBytes int64 = 1024
	actual := RoundUpBytes(sizeInBytes)
	if actual != 1*GiB {
		t.Fatalf("Wrong result for RoundUpBytes. Got: %d", actual)
	}
}

func TestRoundUpGiB(t *testing.T) {
	var sizeInBytes int64 = 1
	actual := RoundUpGiB(sizeInBytes)
	if actual != 1 {
		t.Fatalf("Wrong result for RoundUpGiB. Got: %d", actual)
	}
}

func TestBytesToGiB(t *testing.T) {
	var sizeInBytes int64 = 5 * GiB

	actual := BytesToGiB(sizeInBytes)
	if actual != 5 {
		t.Fatalf("Wrong result for BytesToGiB. Got: %d", actual)
	}
}

func TestGiBToBytes(t *testing.T) {
	var sizeInGiB int64 = 3

	actual := GiBToBytes(sizeInGiB)
	if actual != 3*GiB {
		t.Fatalf("Wrong result for GiBToBytes. Got: %d", actual)
	}
}

func TestGetAzcopyJob(t *testing.T) {
	tests := []struct {
		desc             string
		listStr          string
		listErr          error
		enableShow       bool
		showStr          string
		showErr          error
		expectedJobState AzcopyJobState
		expectedPercent  string
		expectedErr      error
	}{
		{
			desc:             "run exec get error",
			listStr:          "",
			listErr:          fmt.Errorf("error"),
			enableShow:       false,
			showStr:          "",
			showErr:          nil,
			expectedJobState: AzcopyJobError,
			expectedPercent:  "",
			expectedErr:      fmt.Errorf("couldn't list jobs in azcopy error"),
		},
		{
			desc:             "run exec parse azcopy job list get error",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC",
			listErr:          nil,
			enableShow:       false,
			showStr:          "",
			showErr:          nil,
			expectedJobState: AzcopyJobError,
			expectedPercent:  "",
			expectedErr:      fmt.Errorf("couldn't parse azcopy job list in azcopy error parsing jobs list: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC"),
		},
		{
			desc:             "run exec parse azcopy job not found jobid when Status is Canceled",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: Cancelled\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			listErr:          nil,
			enableShow:       false,
			showStr:          "",
			showErr:          nil,
			expectedJobState: AzcopyJobNotFound,
			expectedPercent:  "",
			expectedErr:      nil,
		},
		{
			desc:             "run exec parse azcopy job Completed",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: Completed\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			listErr:          nil,
			enableShow:       false,
			showStr:          "",
			showErr:          nil,
			expectedJobState: AzcopyJobCompleted,
			expectedPercent:  "100.0",
			expectedErr:      nil,
		},
		{
			desc:             "run exec get error in azcopy jobs show",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			listErr:          nil,
			enableShow:       true,
			showStr:          "",
			showErr:          fmt.Errorf("error"),
			expectedJobState: AzcopyJobError,
			expectedPercent:  "",
			expectedErr:      fmt.Errorf("couldn't show jobs summary in azcopy error"),
		},
		{
			desc:             "run exec parse azcopy job show error",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			listErr:          nil,
			enableShow:       true,
			showStr:          "",
			showErr:          nil,
			expectedJobState: AzcopyJobError,
			expectedPercent:  "",
			expectedErr:      fmt.Errorf("couldn't parse azcopy job show in azcopy error parsing jobs summary:  in Percent Complete (approx)"),
		},
		{
			desc:             "run exec parse azcopy job show succeed",
			listStr:          "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			listErr:          nil,
			enableShow:       true,
			showStr:          "Percent Complete (approx): 50.0",
			showErr:          nil,
			expectedJobState: AzcopyJobRunning,
			expectedPercent:  "50.0",
			expectedErr:      nil,
		},
	}
	for _, test := range tests {
		dstFileshare := "dstFileshare"

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		m := NewMockEXEC(ctrl)
		m.EXPECT().RunCommand(gomock.Eq("azcopy jobs list | grep dstFileshare -B 3"), []string{}).Return(test.listStr, test.listErr)
		if test.enableShow {
			m.EXPECT().RunCommand(gomock.Not("azcopy jobs list | grep dstFileshare -B 3"), []string{}).Return(test.showStr, test.showErr)
		}

		azcopyFunc := &Azcopy{}
		azcopyFunc.ExecCmd = m
		jobState, percent, err := azcopyFunc.GetAzcopyJob(dstFileshare, []string{})
		if jobState != test.expectedJobState || percent != test.expectedPercent || !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected jobState: %v, percent: %v, err: %v, expected jobState: %v, percent: %v, err: %v", test.desc, jobState, percent, err, test.expectedJobState, test.expectedPercent, test.expectedErr)
		}
	}
}

func TestParseAzcopyJobList(t *testing.T) {
	tests := []struct {
		desc             string
		str              string
		expectedJobid    string
		expectedJobState AzcopyJobState
		expectedErr      error
	}{
		{
			desc:             "azcopy job not found",
			str:              "",
			expectedJobid:    "",
			expectedJobState: AzcopyJobNotFound,
			expectedErr:      nil,
		},
		{
			desc:             "parse azcopy job list error",
			str:              "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC",
			expectedJobid:    "",
			expectedJobState: AzcopyJobError,
			expectedErr:      fmt.Errorf("error parsing jobs list: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC"),
		},
		{
			desc:             "parse azcopy job list status error",
			str:              "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus Cancelled\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			expectedJobid:    "",
			expectedJobState: AzcopyJobError,
			expectedErr:      fmt.Errorf("error parsing jobs list status: Status Cancelled"),
		},
		{
			desc:             "parse azcopy job not found jobid when Status is Canceled",
			str:              "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: Cancelled\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			expectedJobid:    "",
			expectedJobState: AzcopyJobNotFound,
			expectedErr:      nil,
		},
		{
			desc:             "parse azcopy job Completed",
			str:              "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: Completed\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			expectedJobid:    "",
			expectedJobState: AzcopyJobCompleted,
			expectedErr:      nil,
		},
		{
			desc:             "parse azcopy job InProgress",
			str:              "JobId: ed1c3833-eaff-fe42-71d7-513fb065a9d9\nStart Time: Monday, 07-Aug-23 03:29:54 UTC\nStatus: InProgress\nCommand: copy https://{accountName}.file.core.windows.net/{srcFileshare}{SAStoken} https://{accountName}.file.core.windows.net/{dstFileshare}{SAStoken} --recursive --check-length=false",
			expectedJobid:    "ed1c3833-eaff-fe42-71d7-513fb065a9d9",
			expectedJobState: AzcopyJobRunning,
			expectedErr:      nil,
		},
	}

	for _, test := range tests {
		jobid, jobState, err := parseAzcopyJobList(test.str)
		if jobid != test.expectedJobid || jobState != test.expectedJobState || !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected jobid: %v, jobState: %v, err: %v, expected jobid: %v, jobState: %v, err: %v", test.desc, jobid, jobState, err, test.expectedJobid, test.expectedJobState, test.expectedErr)
		}
	}
}

func TestParseAzcopyJobShow(t *testing.T) {
	tests := []struct {
		desc             string
		str              string
		expectedJobState AzcopyJobState
		expectedPercent  string
		expectedErr      error
	}{
		{
			desc:             "error parse azcopy job show",
			str:              "",
			expectedJobState: AzcopyJobError,
			expectedPercent:  "",
			expectedErr:      fmt.Errorf("error parsing jobs summary:  in Percent Complete (approx)"),
		},
		{
			desc:             "parse azcopy job show succeed",
			str:              "Percent Complete (approx): 50.0",
			expectedJobState: AzcopyJobRunning,
			expectedPercent:  "50.0",
			expectedErr:      nil,
		},
	}

	for _, test := range tests {
		jobState, percent, err := parseAzcopyJobShow(test.str)
		if jobState != test.expectedJobState || percent != test.expectedPercent || !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("test[%s]: unexpected jobState: %v, percent: %v, err: %v, expected jobState: %v, percent: %v, err: %v", test.desc, jobState, percent, err, test.expectedJobState, test.expectedPercent, test.expectedErr)
		}
	}
}

func TestWaitUntilTimeout(t *testing.T) {
	tests := []struct {
		desc        string
		timeout     time.Duration
		execFunc    ExecFunc
		timeoutFunc TimeoutFunc
		expectedErr error
	}{
		{
			desc:    "execFunc returns error",
			timeout: 1 * time.Second,
			execFunc: func() error {
				return fmt.Errorf("execFunc error")
			},
			timeoutFunc: func() error {
				return fmt.Errorf("timeout error")
			},
			expectedErr: fmt.Errorf("execFunc error"),
		},
		{
			desc:    "execFunc timeout",
			timeout: 1 * time.Second,
			execFunc: func() error {
				time.Sleep(2 * time.Second)
				return nil
			},
			timeoutFunc: func() error {
				return fmt.Errorf("timeout error")
			},
			expectedErr: fmt.Errorf("timeout error"),
		},
		{
			desc:    "execFunc completed successfully",
			timeout: 1 * time.Second,
			execFunc: func() error {
				return nil
			},
			timeoutFunc: func() error {
				return fmt.Errorf("timeout error")
			},
			expectedErr: nil,
		},
	}

	for _, test := range tests {
		err := WaitUntilTimeout(test.timeout, test.execFunc, test.timeoutFunc)
		if err != nil && (err.Error() != test.expectedErr.Error()) {
			t.Errorf("unexpected error: %v, expected error: %v", err, test.expectedErr)
		}
	}
}
