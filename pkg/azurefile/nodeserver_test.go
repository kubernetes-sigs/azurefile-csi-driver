/*
Copyright 2020 The Kubernetes Authors.

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

package azurefile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	volume "github.com/kata-containers/kata-containers/src/runtime/pkg/direct-volume"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	clientset "k8s.io/client-go/kubernetes"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/testutil"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/accountclient/mock_accountclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/mock_azclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider/storage"
)

const (
	sourceTest = "./source_test"
	targetTest = "./target_test"
)

type ExecArgs struct {
	command string
	args    []string
	output  string
	err     error
}

func matchFlakyWindowsError(mainError error, substr string) bool {
	var errorMessage string
	if mainError == nil {
		errorMessage = ""
	} else {
		errorMessage = mainError.Error()
	}

	return strings.Contains(errorMessage, substr)
}

func TestNodeGetInfo(t *testing.T) {
	d := NewFakeDriver()

	// Test valid request
	req := csi.NodeGetInfoRequest{}
	resp, err := d.NodeGetInfo(context.Background(), &req)
	assert.NoError(t, err)
	assert.Equal(t, resp.GetNodeId(), fakeNodeID)
}

func TestNodeGetCapabilities(t *testing.T) {
	d := NewFakeDriver()
	capType := &csi.NodeServiceCapability_Rpc{
		Rpc: &csi.NodeServiceCapability_RPC{
			Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		},
	}
	capVolumeStats := &csi.NodeServiceCapability_Rpc{
		Rpc: &csi.NodeServiceCapability_RPC{
			Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		},
	}

	capVolumeMountGroup := &csi.NodeServiceCapability_Rpc{
		Rpc: &csi.NodeServiceCapability_RPC{
			Type: csi.NodeServiceCapability_RPC_VOLUME_MOUNT_GROUP,
		},
	}
	capList := []*csi.NodeServiceCapability{
		{Type: capType},
		{Type: capVolumeStats},
		{Type: capVolumeMountGroup},
	}
	d.NSCap = capList
	// Test valid request
	req := csi.NodeGetCapabilitiesRequest{}
	resp, err := d.NodeGetCapabilities(context.Background(), &req)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.Capabilities[0].GetType(), capType)
	assert.Equal(t, resp.Capabilities[1].GetType(), capVolumeStats)
	assert.Equal(t, resp.Capabilities[2].GetType(), capVolumeMountGroup)
	assert.NoError(t, err)
}

func mockGetRuntimeClassForPod(_ context.Context, _ clientset.Interface, _, _ string) (string, error) {
	return "mockRuntimeClass", nil
}

func mockIsConfidentialRuntimeClass(_ context.Context, _ clientset.Interface, _ string, _ string) (bool, error) {
	return true, nil
}

func TestNodePublishVolume(t *testing.T) {
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{}
	volumeCap := csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER}
	var (
		errorMountSource     = testutil.GetWorkDirPath("error_mount_source", t)
		alreadyMountedTarget = testutil.GetWorkDirPath("false_is_likely_exist_target", t)
		azureFile            = testutil.GetWorkDirPath("azure.go", t)

		sourceTest = testutil.GetWorkDirPath("source_test", t)
		targetTest = testutil.GetWorkDirPath("target_test", t)
	)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDirectVolume := NewMockDirectVolume(ctrl)
	getRuntimeClassForPodFunc = mockGetRuntimeClassForPod
	isConfidentialRuntimeClassFunc = mockIsConfidentialRuntimeClass
	d.isKataNode = false

	tests := []struct {
		desc        string
		setup       func()
		req         *csi.NodePublishVolumeRequest
		expectedErr testutil.TestError
		cleanup     func()
	}{
		{
			desc: "[Error] Volume capabilities missing",
			req:  &csi.NodePublishVolumeRequest{},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume capability missing in request"),
			},
		},
		{
			desc: "[Error] Volume ID missing",
			req:  &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap}},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			},
		},
		{
			desc: "[Error] Target path missing",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Target path not provided"),
			},
		},
		{
			desc: "[Error] Stage target path missing",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:   "vol_1",
				TargetPath: targetTest},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Staging target not provided"),
			},
		},
		{
			desc: "[Error] Not a directory",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        azureFile,
				StagingTargetPath: sourceTest,
				Readonly:          true},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "Could not mount target %s: mkdir %s: not a directory", azureFile, azureFile),
				WindowsError: status.Errorf(codes.Internal, "Could not mount target %v: mkdir %s: The system cannot find the path specified.", azureFile, azureFile),
			},
		},
		{
			desc: "[Error] Mount error mocked by Mount",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest,
				StagingTargetPath: errorMountSource,
				Readonly:          true},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "Could not mount %s at %s: fake Mount: source error", errorMountSource, targetTest),
			},
		},
		{
			desc: "[Error] Failed to get account for Ephemeral Volumes",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "testrg#testAccount#testFileShare#testuuid",
				TargetPath:        targetTest,
				StagingTargetPath: sourceTest,
				Readonly:          true,
				VolumeContext:     map[string]string{ephemeralField: "true"},
			},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, fmt.Sprintf("failed to get account name from %s", "testrg#testAccount#testFileShare#testuuid")),
			},
		},
		{
			desc: "[Success] Valid request read only",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest,
				StagingTargetPath: sourceTest,
				Readonly:          true,
				VolumeContext:     map[string]string{mountPermissionsField: "0755"},
			},
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Success] Valid request already mounted",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        alreadyMountedTarget,
				StagingTargetPath: sourceTest,
				Readonly:          true},
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Success] Valid request",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest,
				StagingTargetPath: sourceTest,
				Readonly:          true},
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Error] invalid mountPermissions",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest,
				StagingTargetPath: sourceTest,
				VolumeContext:     map[string]string{mountPermissionsField: "07ab"},
			},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s", "07ab")),
			},
		},
		{
			desc: "[Success] Valid request with Kata CC Mount enabled",
			req: &csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
				VolumeId:          "vol_1",
				TargetPath:        targetTest,
				StagingTargetPath: sourceTest,
				Readonly:          true,
				VolumeContext:     map[string]string{mountPermissionsField: "0755", podNameField: "testPod", podNamespaceField: "testNamespace"},
			},
			setup: func() {
				d.isKataNode = true
				d.directVolume = mockDirectVolume
				mockDirectVolume.EXPECT().VolumeMountInfo(sourceTest).Return(&volume.MountInfo{}, nil)
				mockDirectVolume.EXPECT().Add(targetTest, gomock.Any()).Return(nil)
			},
			cleanup: func() {
			},
		},
	}

	// Setup
	_ = makeDir(alreadyMountedTarget, 0755)
	mounter, err := NewFakeMounter()
	if err != nil {
		t.Fatalf("failed to get fake mounter: %v", err)
	}
	if runtime.GOOS != "windows" {
		mounter.Exec = &testingexec.FakeExec{ExactOrder: true}
	}
	d.mounter = mounter

	for _, test := range tests {
		if test.setup != nil {
			test.setup()
		}
		_, err := d.NodePublishVolume(context.Background(), test.req)
		if !testutil.AssertError(err, &test.expectedErr) {
			t.Errorf("test case: %s, \nUnexpected error: %v\nExpected error: %v", test.desc, err, test.expectedErr.GetExpectedError())
		}
		if test.cleanup != nil {
			test.cleanup()
		}
	}

	// Clean up
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
	err = os.RemoveAll(alreadyMountedTarget)
	assert.NoError(t, err)
}

func TestNodeUnpublishVolume(t *testing.T) {
	errorTarget := testutil.GetWorkDirPath("error_is_likely_target", t)
	targetFile := testutil.GetWorkDirPath("abc.go", t)
	d := NewFakeDriver()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDirectVolume := NewMockDirectVolume(ctrl)
	d.directVolume = mockDirectVolume

	tests := []struct {
		desc         string
		setup        func()
		req          *csi.NodeUnpublishVolumeRequest
		skipOnDarwin bool
		expectedErr  testutil.TestError
		cleanup      func()
	}{
		{
			desc: "[Error] Volume ID missing",
			req:  &csi.NodeUnpublishVolumeRequest{TargetPath: targetTest},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			},
		},
		{
			desc: "[Error] Target missing",
			req:  &csi.NodeUnpublishVolumeRequest{VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Target path missing in request"),
			},
		},
		{
			desc:         "[Error] Unmount error mocked by IsLikelyNotMountPoint",
			skipOnDarwin: true,
			req:          &csi.NodeUnpublishVolumeRequest{TargetPath: errorTarget, VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Internal, fmt.Sprintf("failed to unmount target %s: fake IsLikelyNotMountPoint: fake error", errorTarget)),
			},
			setup: func() {
				if runtime.GOOS == "windows" {
					d.isKataNode = true
					mockDirectVolume.EXPECT().Remove(errorTarget).Return(nil)
				}
			},
		},
		{
			desc: "[Success] Valid request",
			req:  &csi.NodeUnpublishVolumeRequest{TargetPath: targetFile, VolumeId: "vol_1"},
			setup: func() {
				d.isKataNode = true
				mockDirectVolume.EXPECT().Remove(targetFile).Return(nil)
			},
			expectedErr: testutil.TestError{},
		},
	}

	// Setup
	_ = makeDir(errorTarget, 0755)
	mounter, err := NewFakeMounter()
	if err != nil {
		t.Fatalf("failed to get fake mounter: %v", err)
	}
	if runtime.GOOS != "windows" {
		mounter.Exec = &testingexec.FakeExec{ExactOrder: true}
	}
	d.mounter = mounter

	for _, test := range tests {
		if test.setup != nil {
			test.setup()
		}
		if test.skipOnDarwin && runtime.GOOS == "darwin" {
			continue
		}
		_, err := d.NodeUnpublishVolume(context.Background(), test.req)
		if !testutil.AssertError(err, &test.expectedErr) {
			t.Errorf("test case: %s, \nUnexpected error: %v\nExpected error: %v", test.desc, err, test.expectedErr.GetExpectedError())
		}
		if test.cleanup != nil {
			test.cleanup()
		}
	}

	// Clean up
	err = os.RemoveAll(errorTarget)
	assert.NoError(t, err)
}

func TestNodeStageVolume(t *testing.T) {
	stdVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
	}
	groupVolCap := csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{
				VolumeMountGroup: "test_vmgroup",
			},
		},
	}

	var (
		errorMountSensSource   = testutil.GetWorkDirPath("error_mount_sens_source", t)
		sourceTest             = testutil.GetWorkDirPath("source_test", t)
		azureStagingTargetPath = testutil.GetWorkDirPath("azure.go", t)
		proxyMountPath         = testutil.GetWorkDirPath("proxy-mount", t)
		testDiskPath           = fmt.Sprintf("%s/test_disk.vhd", proxyMountPath)
	)

	volContextEmptyDiskName := map[string]string{
		fsTypeField:     "ext4",
		protocolField:   "nfs",
		diskNameField:   "",
		shareNameField:  "test_sharename",
		serverNameField: "test_servername",
		folderNameField: "test_folder",
	}
	volContextEmptyShareName := map[string]string{
		fsTypeField:     "smb",
		diskNameField:   "test_disk.vhd",
		shareNameField:  "test_sharename",
		serverNameField: "",
	}
	volContextNfs := map[string]string{
		fsTypeField:           "nfs",
		diskNameField:         "test_disk.vhd",
		shareNameField:        "test_sharename",
		serverNameField:       "test_servername",
		mountPermissionsField: "0755",
	}
	volContext := map[string]string{
		fsTypeField:           "smb",
		diskNameField:         "test_disk.vhd",
		shareNameField:        "test_sharename",
		serverNameField:       "test_servername",
		mountPermissionsField: "0755",
	}
	volContextFsType := map[string]string{
		fsTypeField:     "ext4",
		diskNameField:   "test_disk.vhd",
		shareNameField:  "test_sharename",
		serverNameField: "test_servername",
	}
	errorSource := `\\test_servername\test_sharename`
	errorSourceNFS := `test_servername://test_sharename`

	secrets := map[string]string{
		"accountname": "k8s",
		"accountkey":  "testkey",
	}
	d := NewFakeDriver()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockResolver := NewMockResolver(ctrl)
	mockDirectVolume := NewMockDirectVolume(ctrl)
	d.isKataNode = false

	tests := []struct {
		desc          string
		setup         func()
		req           *csi.NodeStageVolumeRequest
		execScripts   []ExecArgs
		skipOnDarwin  bool
		skipOnWindows bool
		expectedErr   testutil.TestError
		// use this field only when Windows
		// gives flaky error messages due
		// to CSI proxy
		// This field holds the base error message
		// that is common amongst all other flaky
		// error messages
		flakyWindowsErrorMessage string
		cleanup                  func()
	}{
		{
			desc:        "[Error] Volume ID missing",
			req:         &csi.NodeStageVolumeRequest{},
			execScripts: nil,
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			},
		},
		{
			desc: "[Error] Stage target path missing",
			req:  &csi.NodeStageVolumeRequest{VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Staging target not provided"),
			},
		},
		{
			desc: "[Error] Volume capabilities missing",
			req:  &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume capability not provided"),
			},
		},
		{
			desc: "[Error] GetAccountInfo error parsing volume id",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "failed to get file share name from vol_1"),
			},
		},
		{
			desc: "[Error] Invalid fsType",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					fsTypeField:                "test_fs",
					shareNameField:             "test_sharename",
					serverNameField:            "test_servername",
					storageEndpointSuffixField: ".core",
					pvcNamespaceKey:            "pvcname",
					pvcNameKey:                 "pvc",
					pvNameKey:                  "pv",
				}},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "fsType(test_fs) is not supported, supported fsType list: [cifs smb nfs ext4 ext3 ext2 xfs]"),
			},
		},
		{
			desc: "[Error] Invalid protocol",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					protocolField:   "test_protocol",
					shareNameField:  "test_sharename",
					serverNameField: "test_servername",
				}},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "protocol(test_protocol) is not supported, supported protocol list: [smb nfs]"),
			},
		},
		{
			desc: "[Error] Invalid fsGroupChangePolicy",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					fsGroupChangePolicyField: "test_fsGroupChangePolicy",
					shareNameField:           "test_sharename",
					serverNameField:          "test_servername",
				}},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "fsGroupChangePolicy(test_fsGroupChangePolicy) is not supported, supported fsGroupChangePolicy list: [None Always OnRootMismatch]"),
			},
		},
		{
			desc: "[Error] Invalid mountWithManagedIdentity",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					mountWithManagedIdentityField: "invalid",
					shareNameField:                "test_sharename",
					serverNameField:               "test_servername",
				}},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.InvalidArgument, "GetAccountInfo(vol_1) failed with error: invalid %s: %s in volume context", mountWithManagedIdentityField, "invalid"),
			},
		},
		{
			desc: "[Error] Empty accountname",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					shareNameField:  "test_sharename",
					serverNameField: "test_servername",
				}},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "accountName() or accountKey is empty"),
			},
		},
		{
			desc: "[Error] Volume operation in progress",
			setup: func() {
				d.volumeLocks.TryAcquire(fmt.Sprintf("%s-%s", "vol_1##", sourceTest))
			},
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Aborted, fmt.Sprintf(volumeOperationAlreadyExistsFmt, "vol_1##")),
			},
			cleanup: func() {
				d.volumeLocks.Release(fmt.Sprintf("%s-%s", "vol_1##", sourceTest))
			},
		},
		{
			desc: "[Error] Not a Directory",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: azureStagingTargetPath,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Internal, fmt.Sprintf("MkdirAll %s failed with error: mkdir %s: not a directory", azureStagingTargetPath, azureStagingTargetPath)),
				WindowsError: status.Error(codes.Internal, fmt.Sprintf("Could not mount target %v: mkdir %s: The system cannot find the path specified.", azureStagingTargetPath, azureStagingTargetPath)),
			},
		},
		{
			desc: "[Error] Empty Disk Name",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContextEmptyDiskName,
				Secrets:          secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "diskname could not be empty, targetPath: %s", sourceTest),
			},
		},
		{
			desc: "[Error] FormatAndMount mocked by exec commands with protocol as nfs",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					fsTypeField:       "ext4",
					protocolField:     "nfs",
					diskNameField:     "test_disk.vhd",
					shareNameField:    "test_sharename",
					serverNameField:   "test_servername",
					ephemeralField:    "true",
					mountOptionsField: "test_ephemeral",
				},
				Secrets: secrets},
			execScripts: []ExecArgs{
				{"blkid", []string{"-p", "-s", "TYPE", "-s", "PTTYPE", "-o", "export", testDiskPath}, "", &testingexec.FakeExitError{Status: 2}},
				{"mkfs.ext4", []string{"-F", "-m0", testDiskPath}, "", fmt.Errorf("formatting failed")},
			},
			skipOnDarwin: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"empty mountOptions(len: 1) or sensitiveMountOptions(len: 0) is not allowed",
				errorSourceNFS, proxyMountPath),
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "could not format %v and mount it at %v", sourceTest, testDiskPath),
			},
		},
		{
			desc: "[Error] Failed SMB mount mocked by MountSensitive",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: errorMountSensSource,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			skipOnDarwin:  true,
			skipOnWindows: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed "+
				"with smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, errorMountSensSource),
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "volume(vol_1##) mount //test_servername/test_sharename on %v failed with fake MountSensitive: target error", errorMountSensSource),
			},
		},
		{
			desc: "[Error] FormatAndMount mocked by exec commands",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContextFsType,
				Secrets:          secrets},
			execScripts: []ExecArgs{
				{"blkid", []string{"-p", "-s", "TYPE", "-s", "PTTYPE", "-o", "export", testDiskPath}, "", &testingexec.FakeExitError{Status: 2}},
				{"mkfs.ext4", []string{"-F", "-m0", testDiskPath}, "", fmt.Errorf("formatting failed")},
			},
			skipOnDarwin:  true,
			skipOnWindows: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, proxyMountPath),
			expectedErr: testutil.TestError{
				DefaultError: status.Errorf(codes.Internal, "could not format %v and mount it at %v", sourceTest, testDiskPath),
			},
		},
		{
			desc: "[Success] Valid request",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContext,
				Secrets:          secrets},
			skipOnWindows: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, sourceTest),
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Success] Valid request with share name empty",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContextEmptyShareName,
				Secrets:          secrets},
			skipOnWindows: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount \\\\k8s.file.test_suffix\\test_sharename on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				sourceTest),
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Success] Valid request with fsType as nfs",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContextNfs,
				Secrets:          secrets},
			skipOnWindows: true,
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, sourceTest),
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Success] Valid request with supported fsType disk",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext:    volContextFsType,
				Secrets:          secrets},
			skipOnWindows: true,
			execScripts: []ExecArgs{
				{"blkid", []string{"-p", "-s", "TYPE", "-s", "PTTYPE", "-o", "export", testDiskPath}, "", nil},
				{"mkfs.ext4", []string{"-F", "-m0", testDiskPath}, "", nil},
			},
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, proxyMountPath),
			expectedErr: testutil.TestError{},
		},
		{
			desc: "[Error] ",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &groupVolCap,
				VolumeContext:    volContextFsType,
				Secrets:          secrets},
			skipOnWindows: true,
			execScripts: []ExecArgs{
				{"blkid", []string{"-p", "-s", "TYPE", "-s", "PTTYPE", "-o", "export", testDiskPath}, "", nil},
				{"mkfs.ext4", []string{"-F", "-m0", testDiskPath}, "", nil},
			},
			flakyWindowsErrorMessage: fmt.Sprintf("volume(vol_1##) mount %s on %v failed with "+
				"smb mapping failed with error: rpc error: code = Unknown desc = NewSmbGlobalMapping failed.",
				errorSource, proxyMountPath),
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Internal, fmt.Sprintf("SetVolumeOwnership with volume(vol_1##) on %s failed with %v", proxyMountPath, fmt.Errorf("convert test_vmgroup to int failed with strconv.Atoi: parsing \"test_vmgroup\": invalid syntax"))),
			},
		},
		{
			desc: "[Error] invalid mountPermissions",
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					shareNameField:        "test_sharename",
					mountPermissionsField: "07ab",
				},
				Secrets: secrets},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s", "07ab")),
			},
		},
		{
			desc: "[Success] Valid request with Kata CC Mount enabled",
			setup: func() {
				d.resolver = mockResolver
				d.directVolume = mockDirectVolume
				if runtime.GOOS != "windows" {
					d.isKataNode = true
					mockIPAddr := &net.IPAddr{IP: net.ParseIP("192.168.1.1")}
					mockDirectVolume.EXPECT().VolumeMountInfo(sourceTest).Return(nil, nil)
					mockResolver.EXPECT().ResolveIPAddr("ip", "test_servername").Return(mockIPAddr, nil)
					mockDirectVolume.EXPECT().Add(sourceTest, gomock.Any()).Return(nil)
				}
			},
			req: &csi.NodeStageVolumeRequest{VolumeId: "vol_1##", StagingTargetPath: sourceTest,
				VolumeCapability: &stdVolCap,
				VolumeContext: map[string]string{
					fsTypeField:           "smb",
					diskNameField:         "test_disk.vhd",
					shareNameField:        "test_sharename",
					serverNameField:       "test_servername",
					mountPermissionsField: "0755",
				},
				Secrets: secrets},
			skipOnWindows: true,
		},
	}

	// Setup
	for _, test := range tests {

		if test.setup != nil {
			test.setup()
		}
		if test.skipOnDarwin && runtime.GOOS == "darwin" {
			continue
		}
		if test.skipOnWindows && runtime.GOOS == "windows" {
			continue
		}
		mounter, err := NewFakeMounter()
		if err != nil {
			t.Fatalf("failed to get fake mounter: %v", err)
		}

		if runtime.GOOS != "windows" {
			fakeExec := &testingexec.FakeExec{ExactOrder: true}
			for _, script := range test.execScripts {
				fakeCmd := &testingexec.FakeCmd{}
				cmdAction := makeFakeCmd(fakeCmd, script.command, script.args...)
				outputAction := makeFakeOutput(script.output, script.err)
				fakeCmd.CombinedOutputScript = append(fakeCmd.CombinedOutputScript, outputAction)
				fakeExec.CommandScript = append(fakeExec.CommandScript, cmdAction)
			}
			mounter.Exec = fakeExec
		}

		d.mounter = mounter
		clientFactory := mock_azclient.NewMockClientFactory(ctrl)
		mockAccountClient := mock_accountclient.NewMockInterface(ctrl)
		clientFactory.EXPECT().GetAccountClientForSub(gomock.Any()).Return(mockAccountClient, nil).AnyTimes()
		d.cloud = &storage.AccountRepo{
			Environment:          &azclient.Environment{StorageEndpointSuffix: "test_suffix"},
			NetworkClientFactory: clientFactory,
			ComputeClientFactory: clientFactory,
		}

		_, err = d.NodeStageVolume(context.Background(), test.req)
		// separate assertion for flaky error messages
		if test.flakyWindowsErrorMessage != "" && runtime.GOOS == "windows" {
			if !matchFlakyWindowsError(err, test.flakyWindowsErrorMessage) {
				t.Errorf("test case: %s, \nUnexpected error: %v\nExpected error: %v", test.desc, err, test.flakyWindowsErrorMessage)
			}
		} else {
			if !testutil.AssertError(err, &test.expectedErr) {
				t.Errorf("test case: %s, \nUnexpected error: %v\nExpected error: %v", test.desc, err, test.expectedErr.GetExpectedError())
			}
		}
		if test.cleanup != nil {
			test.cleanup()
		}
	}

	// Clean up
	err := os.RemoveAll(sourceTest)
	assert.NoError(t, err)
	err = os.RemoveAll(proxyMount)
	assert.NoError(t, err)
	err = os.RemoveAll(errorMountSensSource)
	assert.NoError(t, err)
}

func TestNodeUnstageVolume(t *testing.T) {
	var (
		errorTarget = testutil.GetWorkDirPath("error_is_likely_target", t)
		targetFile  = testutil.GetWorkDirPath("abc.go", t)
	)
	d := NewFakeDriver()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	clientFactory := mock_azclient.NewMockClientFactory(ctrl)
	d.cloud = &storage.AccountRepo{
		Environment:          &azclient.Environment{StorageEndpointSuffix: "test_suffix"},
		ComputeClientFactory: clientFactory,
		NetworkClientFactory: clientFactory,
	}
	mockDirectVolume := NewMockDirectVolume(ctrl)
	d.directVolume = mockDirectVolume

	tests := []struct {
		desc         string
		setup        func()
		req          *csi.NodeUnstageVolumeRequest
		skipOnDarwin bool
		expectedErr  testutil.TestError
		cleanup      func()
	}{
		{
			desc: "[Error] Volume ID missing",
			req:  &csi.NodeUnstageVolumeRequest{StagingTargetPath: targetTest},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Volume ID missing in request"),
			},
		},
		{
			desc: "[Error] Target missing",
			req:  &csi.NodeUnstageVolumeRequest{VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.InvalidArgument, "Staging target not provided"),
			},
		},
		{
			desc: "[Error] Volume operation in progress",
			setup: func() {
				d.volumeLocks.TryAcquire(fmt.Sprintf("%s-%s", "vol_1", targetFile))
			},
			req: &csi.NodeUnstageVolumeRequest{StagingTargetPath: targetFile, VolumeId: "vol_1"},
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Aborted, fmt.Sprintf(volumeOperationAlreadyExistsFmt, "vol_1")),
			},
			cleanup: func() {
				d.volumeLocks.Release(fmt.Sprintf("%s-%s", "vol_1", targetFile))
			},
		},
		{
			desc:         "[Error] CleanupMountPoint error mocked by IsLikelyNotMountPoint",
			req:          &csi.NodeUnstageVolumeRequest{StagingTargetPath: errorTarget, VolumeId: "vol_1"},
			skipOnDarwin: true,
			expectedErr: testutil.TestError{
				DefaultError: status.Error(codes.Internal, fmt.Sprintf("failed to unmount staging target %v: fake IsLikelyNotMountPoint: fake error", errorTarget)),
			},
			setup: func() {
				if runtime.GOOS == "windows" {
					d.isKataNode = true
					mockDirectVolume.EXPECT().Remove(errorTarget).Return(nil)
				}
			},
		},
		{
			desc: "[Success] Valid request",
			req:  &csi.NodeUnstageVolumeRequest{StagingTargetPath: targetFile, VolumeId: "vol_1"},
			setup: func() {
				d.isKataNode = true
				mockDirectVolume.EXPECT().Remove(targetFile).Return(nil)
			},
			expectedErr: testutil.TestError{},
		},
	}

	// Setup
	_ = makeDir(errorTarget, 0755)
	mounter, err := NewFakeMounter()
	if err != nil {
		t.Fatalf("failed to get fake mounter: %v", err)
	}
	if runtime.GOOS != "windows" {
		mounter.Exec = &testingexec.FakeExec{ExactOrder: true}
	}
	d.mounter = mounter

	for _, test := range tests {

		if test.setup != nil {
			test.setup()
		}
		if test.skipOnDarwin && runtime.GOOS == "darwin" {
			continue
		}
		_, err := d.NodeUnstageVolume(context.Background(), test.req)
		if !testutil.AssertError(err, &test.expectedErr) {
			t.Errorf("Desc: %v\nUnexcpected error: %v\nExpected: %v", test.desc, err, test.expectedErr.GetExpectedError())
		}
		if test.cleanup != nil {
			test.cleanup()
		}
	}

	// Clean up
	err = os.RemoveAll(errorTarget)
	assert.NoError(t, err)
}

func TestNodeGetVolumeStats(t *testing.T) {
	nonexistedPath := "/not/a/real/directory"
	fakePath := "/tmp/fake-volume-path"

	tests := []struct {
		desc        string
		req         *csi.NodeGetVolumeStatsRequest
		expectedErr error
	}{
		{
			desc:        "[Error] Volume ID missing",
			req:         &csi.NodeGetVolumeStatsRequest{VolumePath: targetTest},
			expectedErr: status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty"),
		},
		{
			desc:        "[Error] VolumePath missing",
			req:         &csi.NodeGetVolumeStatsRequest{VolumeId: "vol_1"},
			expectedErr: status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty"),
		},
		{
			desc:        "[Error] Incorrect volume path",
			req:         &csi.NodeGetVolumeStatsRequest{VolumePath: nonexistedPath, VolumeId: "vol_1"},
			expectedErr: status.Errorf(codes.NotFound, "path /not/a/real/directory does not exist"),
		},
		{
			desc:        "[Success] Standard success",
			req:         &csi.NodeGetVolumeStatsRequest{VolumePath: fakePath, VolumeId: "vol_1"},
			expectedErr: nil,
		},
	}

	// Setup
	_ = makeDir(fakePath, 0755)
	d := NewFakeDriver()
	d.cloud = &storage.AccountRepo{
		Environment: &azclient.Environment{StorageEndpointSuffix: "test_suffix"},
	}
	for _, test := range tests {
		if runtime.GOOS == "darwin" {
			continue
		}
		_, err := d.NodeGetVolumeStats(context.Background(), test.req)
		//t.Errorf("[debug] error: %v\n metrics: %v", err, metrics)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("desc: %v, expected error: %v, actual error: %v", test.desc, test.expectedErr, err)
		}
	}

	// Clean up
	err := os.RemoveAll(fakePath)
	assert.NoError(t, err)
}

func TestEnsureMountPoint(t *testing.T) {
	errorTarget := "./error_is_likely_target"
	alreadyExistTarget := "./false_is_likely_exist_target"
	falseTarget := "./false_is_likely_target"
	azureFile := "./azure.go"

	tests := []struct {
		desc        string
		target      string
		expectedErr error
	}{
		{
			desc:        "[Error] Mocked by IsLikelyNotMountPoint",
			target:      errorTarget,
			expectedErr: fmt.Errorf("fake IsLikelyNotMountPoint: fake error"),
		},
		{
			desc:        "[Error] Error opening file",
			target:      falseTarget,
			expectedErr: &os.PathError{Op: "open", Path: "./false_is_likely_target", Err: syscall.ENOENT},
		},
		{
			desc:        "[Error] Not a directory",
			target:      azureFile,
			expectedErr: &os.PathError{Op: "mkdir", Path: "./azure.go", Err: syscall.ENOTDIR},
		},
		{
			desc:        "[Success] Successful run",
			target:      targetTest,
			expectedErr: nil,
		},
		{
			desc:        "[Success] Already existing mount",
			target:      alreadyExistTarget,
			expectedErr: nil,
		},
	}

	// Setup
	_ = makeDir(alreadyExistTarget, 0755)
	d := NewFakeDriver()
	fakeMounter := &fakeMounter{}
	fakeExec := &testingexec.FakeExec{ExactOrder: true}
	d.mounter = &mount.SafeFormatAndMount{
		Interface: fakeMounter,
		Exec:      fakeExec,
	}

	for _, test := range tests {
		_, err := d.ensureMountPoint(test.target, 0777)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Errorf("[%s]: Unexpected Error: %v, expected error: %v", test.desc, err, test.expectedErr)
		}
	}

	// Clean up
	err := os.RemoveAll(alreadyExistTarget)
	assert.NoError(t, err)
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
}

func TestMakeDir(t *testing.T) {
	//Successfully create directory
	err := makeDir(targetTest, 0755)
	assert.NoError(t, err)

	//Failed case
	err = makeDir("./azure.go", 0755)
	var e *os.PathError
	if !errors.As(err, &e) {
		t.Errorf("Unexpected Error: %v", err)
	}

	// Remove the directory created
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
}

func TestNodeExpandVolume(t *testing.T) {
	d := NewFakeDriver()
	req := csi.NodeExpandVolumeRequest{}
	resp, err := d.NodeExpandVolume(context.Background(), &req)
	assert.Nil(t, resp)
	if !reflect.DeepEqual(err, status.Error(codes.Unimplemented, "")) {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestCheckGidPresentInMountFlags(t *testing.T) {
	tests := []struct {
		desc       string
		MountFlags []string
		result     bool
	}{
		{
			desc:       "[Success] Gid present in mount flags",
			MountFlags: []string{"gid=3000"},
			result:     true,
		},
		{
			desc:       "[Success] Gid not present in mount flags",
			MountFlags: []string{},
			result:     false,
		},
	}

	for _, test := range tests {
		gIDPresent := checkGidPresentInMountFlags(test.MountFlags)
		if gIDPresent != test.result {
			t.Errorf("[%s]: Expected result : %t, Actual result: %t", test.desc, test.result, gIDPresent)
		}
	}

}

func TestNodePublishVolumeIdempotentMount(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() != 0 {
		return
	}
	_ = makeDir(sourceTest, 0755)
	_ = makeDir(targetTest, 0755)
	d := NewFakeDriver()
	d.mounter = &mount.SafeFormatAndMount{
		Interface: mount.New(""),
		Exec:      exec.New(),
	}

	volumeCap := csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER}
	req := csi.NodePublishVolumeRequest{VolumeCapability: &csi.VolumeCapability{AccessMode: &volumeCap},
		VolumeId:          "vol_1",
		TargetPath:        targetTest,
		StagingTargetPath: sourceTest,
		Readonly:          true}

	_, err := d.NodePublishVolume(context.Background(), &req)
	assert.NoError(t, err)
	_, err = d.NodePublishVolume(context.Background(), &req)
	assert.NoError(t, err)

	// ensure the target not be mounted twice
	targetAbs, err := filepath.Abs(targetTest)
	assert.NoError(t, err)

	mountList, err := d.mounter.List()
	assert.NoError(t, err)
	mountPointNum := 0
	for _, mountPoint := range mountList {
		if mountPoint.Path == targetAbs {
			mountPointNum++
		}
	}
	assert.Equal(t, 1, mountPointNum)
	err = d.mounter.Unmount(targetTest)
	assert.NoError(t, err)
	_ = d.mounter.Unmount(targetTest)
	err = os.RemoveAll(sourceTest)
	assert.NoError(t, err)
	err = os.RemoveAll(targetTest)
	assert.NoError(t, err)
}

func makeFakeCmd(fakeCmd *testingexec.FakeCmd, cmd string, args ...string) testingexec.FakeCommandAction {
	c := cmd
	a := args
	return func(_ string, _ ...string) exec.Cmd {
		return testingexec.InitFakeCmd(fakeCmd, c, a...)
	}
}

func makeFakeOutput(output string, err error) testingexec.FakeAction {
	o := output
	return func() ([]byte, []byte, error) {
		return []byte(o), nil, err
	}
}
