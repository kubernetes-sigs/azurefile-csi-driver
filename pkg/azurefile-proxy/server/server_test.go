/*
Copyright 2021 The Kubernetes Authors.

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

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	mount_utils "k8s.io/mount-utils"
	mount_azurefile "sigs.k8s.io/azurefile-csi-driver/pkg/azurefile-proxy/pb"
)

func TestServerMountAzureFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		args mount_azurefile.MountAzureFileRequest
		code codes.Code
	}{
		{
			name: "failed_mount",
			args: mount_azurefile.MountAzureFileRequest{
				Source:           "source",
				Target:           "target",
				Fstype:           "nfs",
				MountOptions:     []string{"actimeo=30", "vers=4.1"},
				SensitiveOptions: []string{},
			},
			code: codes.InvalidArgument,
		},
	}

	for i := range testCases {
		tc := &testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mountServer := NewMountServiceServer()
			req := mount_azurefile.MountAzureFileRequest{
				Source:           tc.args.Source,
				Target:           tc.args.Target,
				Fstype:           tc.args.Fstype,
				MountOptions:     tc.args.MountOptions,
				SensitiveOptions: tc.args.SensitiveOptions,
			}
			res, err := mountServer.MountAzureFile(context.Background(), &req)
			if tc.code == codes.OK {
				require.NoError(t, err)
				require.NotNil(t, res)
			} else {
				require.Error(t, err)
				require.Nil(t, res)
			}
		})
	}
}

// slowMounter simulates a mount operation that takes longer than the timeout
type slowMounter struct {
	mount_utils.FakeMounter
	mountDelay time.Duration
}

func (m *slowMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	time.Sleep(m.mountDelay)
	return nil
}

func TestServerMountAzureFileTimeout(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		args        mount_azurefile.MountAzureFileRequest
		mountDelay  time.Duration
		expectError bool
		errorMsg    string
	}{
		{
			name: "mount_timeout_exceeded",
			args: mount_azurefile.MountAzureFileRequest{
				Source:           "source",
				Target:           "target",
				Fstype:           "nfs",
				MountOptions:     []string{"actimeo=30", "vers=4.1"},
				SensitiveOptions: []string{},
			},
			mountDelay:  95 * time.Second, // Exceeds 90 second timeout
			expectError: true,
			errorMsg:    "time out",
		},
		{
			name: "mount_completes_within_timeout",
			args: mount_azurefile.MountAzureFileRequest{
				Source:           "source",
				Target:           "target",
				Fstype:           "nfs",
				MountOptions:     []string{"actimeo=30", "vers=4.1"},
				SensitiveOptions: []string{},
			},
			mountDelay:  1 * time.Second, // Well within 90 second timeout
			expectError: false,
		},
	}

	for i := range testCases {
		tc := &testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Create mount server with slow mounter
			mountServer := &MountServer{
				mounter: &slowMounter{
					mountDelay: tc.mountDelay,
				},
			}

			req := mount_azurefile.MountAzureFileRequest{
				Source:           tc.args.Source,
				Target:           tc.args.Target,
				Fstype:           tc.args.Fstype,
				MountOptions:     tc.args.MountOptions,
				SensitiveOptions: tc.args.SensitiveOptions,
			}

			res, err := mountServer.MountAzureFile(context.Background(), &req)

			if tc.expectError {
				require.Error(t, err)
				require.Nil(t, res)
				require.Contains(t, err.Error(), tc.errorMsg)
			} else {
				require.NoError(t, err)
				require.NotNil(t, res)
			}
		})
	}
}
