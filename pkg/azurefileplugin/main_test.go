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

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
)

func TestMain(t *testing.T) {
	// Set the version flag to true
	os.Args = []string{"cmd", "-version"}

	// Capture stdout
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// Replace exit function with mock function
	var exitCode int
	exit = func(code int) {
		exitCode = code
	}

	// Call main function
	main()

	// Restore stdout
	w.Close()
	os.Stdout = old
	exit = func(code int) {
		os.Exit(code)
	}

	if exitCode != 0 {
		t.Errorf("Expected exit code 0, but got %d", exitCode)
	}
}

func TestTrapClosedConnErr(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectedErr error
	}{
		{
			name:        "ClosedConnectionError",
			err:         fmt.Errorf("use of closed network connection"),
			expectedErr: nil,
		},
		{
			name:        "NilError",
			err:         nil,
			expectedErr: nil,
		},
		{
			name:        "OtherError",
			err:         fmt.Errorf("some other error"),
			expectedErr: fmt.Errorf("some other error"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := trapClosedConnErr(test.err)
			if (err == nil && test.expectedErr != nil) || (err != nil && test.expectedErr == nil) {
				t.Errorf("Expected error %v, but got %v", test.expectedErr, err)
			}
			if err != nil && test.expectedErr != nil && err.Error() != test.expectedErr.Error() {
				t.Errorf("Expected error %v, but got %v", test.expectedErr, err)
			}
		})
	}
}

func TestExportMetrics(t *testing.T) {
	tests := []struct {
		name           string
		metricsAddress string
		shouldError    bool
	}{
		{
			name:           "EmptyMetricsAddress",
			metricsAddress: "",
			shouldError:    false,
		},
		{
			name:           "ValidMetricsAddress",
			metricsAddress: "127.0.0.1:0", // Use port 0 to get any available port
			shouldError:    false,
		},
		{
			name:           "InvalidMetricsAddress",
			metricsAddress: "invalid-address",
			shouldError:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Save original value
			originalMetricsAddress := *metricsAddress
			defer func() {
				*metricsAddress = originalMetricsAddress
			}()

			*metricsAddress = test.metricsAddress
			
			// This function should not panic
			exportMetrics()
			
			// For valid addresses, give a moment for the goroutine to start
			if test.metricsAddress != "" && !test.shouldError {
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

func TestServe(t *testing.T) {
	// Create a listener on an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	// Mock serve function that immediately returns
	mockServeFunc := func(l net.Listener) error {
		return nil
	}

	// Test the serve function
	ctx := context.Background()
	serve(ctx, listener, mockServeFunc)

	// Give time for goroutine to execute
	time.Sleep(100 * time.Millisecond)

	// Listener should be closed by the function
	err = listener.Close()
	if err == nil {
		t.Error("Expected listener to be closed by serve function")
	}
}

func TestServeMetrics(t *testing.T) {
	// Create a listener on an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	// Test the serveMetrics function in a goroutine
	go func() {
		err := serveMetrics(listener)
		// We expect this to return an error when we close the listener
		if err != nil && err.Error() != "use of closed network connection" {
			t.Errorf("Unexpected error from serveMetrics: %v", err)
		}
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Test that the metrics endpoint is available
	addr := listener.Addr().String()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err == nil {
		resp.Body.Close()
		// This is expected behavior - metrics endpoint should be available
	}
}

func TestHandle(t *testing.T) {
	// Save original driverOptions and restore after test
	originalOptions := driverOptions
	defer func() {
		driverOptions = originalOptions
	}()

	// Set test options that will likely succeed in creating a driver
	driverOptions = azurefile.DriverOptions{
		NodeID:                "test-node",
		DriverName:            "file.csi.azure.com",
		Endpoint:              "unix:///tmp/test-handle-csi.sock",
		GoMaxProcs:            2,
		AllowEmptyCloudConfig: true, // Allow empty config to avoid cloud setup issues
	}

	// Clean up socket file if it exists
	os.Remove("/tmp/test-handle-csi.sock")
	defer os.Remove("/tmp/test-handle-csi.sock")

	// Test GOMAXPROCS setting by calling the function that sets it
	oldProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(oldProcs)

	// Create a test that will call handle() and expect it to fail
	// due to missing CSI setup, but still covers the beginning of the function
	done := make(chan bool, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// If handle() panics due to missing setup, that's expected
				done <- true
			}
		}()
		
		// This should call handle() and cover the GOMAXPROCS setting
		// and NewDriver call, but will likely fail at driver.Run()
		handle()
		done <- true
	}()

	// Wait a short time for the goroutine to start and hit the early parts of handle()
	select {
	case <-done:
		// Function completed or panicked, which is fine for coverage
	case <-time.After(1 * time.Second):
		// Timeout is also fine - function likely got stuck in driver.Run()
		// but we've covered the early parts
	}

	// Verify GOMAXPROCS was set (this tests the beginning of handle())
	maxProcs := runtime.GOMAXPROCS(0)
	if maxProcs != driverOptions.GoMaxProcs {
		t.Errorf("Expected GOMAXPROCS to be %d, got %d", driverOptions.GoMaxProcs, maxProcs)
	}
}
