/*
Copyright 2024 The Kubernetes Authors.

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

package pb

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMountAzureFileRequest(t *testing.T) {
	req := &MountAzureFileRequest{
		Source:           "/source/path",
		Target:           "/target/path",
		Fstype:           "cifs",
		MountOptions:     []string{"ro", "noexec"},
		SensitiveOptions: []string{"username=test", "password=secret"},
	}

	// Test getter methods
	if req.GetSource() != "/source/path" {
		t.Errorf("Expected source '/source/path', got '%s'", req.GetSource())
	}

	if req.GetTarget() != "/target/path" {
		t.Errorf("Expected target '/target/path', got '%s'", req.GetTarget())
	}

	if req.GetFstype() != "cifs" {
		t.Errorf("Expected fstype 'cifs', got '%s'", req.GetFstype())
	}

	mountOptions := req.GetMountOptions()
	if len(mountOptions) != 2 || mountOptions[0] != "ro" || mountOptions[1] != "noexec" {
		t.Errorf("Expected mount options ['ro', 'noexec'], got %v", mountOptions)
	}

	sensitiveOptions := req.GetSensitiveOptions()
	if len(sensitiveOptions) != 2 || sensitiveOptions[0] != "username=test" || sensitiveOptions[1] != "password=secret" {
		t.Errorf("Expected sensitive options ['username=test', 'password=secret'], got %v", sensitiveOptions)
	}

	// Test String() method
	str := req.String()
	if str == "" {
		t.Error("String() method should return non-empty string")
	}

	// Test Reset() method
	req.Reset()
	if req.GetSource() != "" || req.GetTarget() != "" || req.GetFstype() != "" {
		t.Error("Reset() should clear all fields")
	}
}

func TestMountAzureFileResponse(t *testing.T) {
	resp := &MountAzureFileResponse{}

	// Test String() method - it's ok if it returns empty string for empty struct
	str := resp.String()
	// Just ensure it doesn't panic - empty string is acceptable
	_ = str

	// Test Reset() method - should not panic
	resp.Reset()
	// No fields to verify after reset since MountAzureFileResponse has no public fields
}

func TestUnimplementedMountServiceServer(t *testing.T) {
	server := &UnimplementedMountServiceServer{}

	// Test that the unimplemented method returns proper error
	req := &MountAzureFileRequest{}
	resp, err := server.MountAzureFile(context.Background(), req)

	if resp != nil {
		t.Error("Expected nil response from unimplemented method")
	}

	if err == nil {
		t.Error("Expected error from unimplemented method")
	}

	// Verify it's the correct gRPC error
	st, ok := status.FromError(err)
	if !ok {
		t.Error("Expected gRPC status error")
	}

	if st.Code() != codes.Unimplemented {
		t.Errorf("Expected Unimplemented error code, got %v", st.Code())
	}
}

// Mock client connection for testing
type mockClientConn struct {
	grpc.ClientConnInterface
	invokeFunc func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error
}

func (m *mockClientConn) Invoke(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
	if m.invokeFunc != nil {
		return m.invokeFunc(ctx, method, args, reply, opts...)
	}
	return nil
}

func TestMountServiceClient(t *testing.T) {
	// Test successful call
	mockConn := &mockClientConn{
		invokeFunc: func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
			if method != "/MountService/MountAzureFile" {
				t.Errorf("Expected method '/MountService/MountAzureFile', got '%s'", method)
			}
			
			// Just return success - response has no fields to set
			return nil
		},
	}

	client := NewMountServiceClient(mockConn)
	req := &MountAzureFileRequest{
		Source: "/test",
		Target: "/mount",
	}

	resp, err := client.MountAzureFile(context.Background(), req)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Error("Expected non-nil response")
	}

	// Test error case
	mockConnError := &mockClientConn{
		invokeFunc: func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
			return status.Error(codes.Internal, "test error")
		},
	}

	clientError := NewMountServiceClient(mockConnError)
	resp, err = clientError.MountAzureFile(context.Background(), req)

	if err == nil {
		t.Error("Expected error from client call")
	}

	if resp != nil {
		t.Error("Expected nil response on error")
	}
}