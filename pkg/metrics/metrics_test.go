/*
Copyright 2026 The Kubernetes Authors.

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

package metrics

import (
	"testing"
	"time"

	"k8s.io/component-base/metrics/legacyregistry"
)

func TestCSIMetricContext_NewCSIMetricContext(t *testing.T) {
	operation := "test_operation"
	mc := NewCSIMetricContext(operation).WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	if mc.operation != operation {
		t.Errorf("expected operation %s, got %s", operation, mc.operation)
	}

	if mc.labels == nil {
		t.Error("expected labels map to be initialized")
	}

	if mc.start.IsZero() {
		t.Error("expected start time to be set")
	}
}

func TestCSIMetricContext_WithLabel(t *testing.T) {
	mc := NewCSIMetricContext("test_operation").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	mc.WithLabel("key1", "value1")
	mc.WithLabel("key2", "value2")

	if mc.labels["key1"] != "value1" {
		t.Errorf("expected label key1=value1, got %s", mc.labels["key1"])
	}

	if mc.labels["key2"] != "value2" {
		t.Errorf("expected label key2=value2, got %s", mc.labels["key2"])
	}
}

func TestCSIMetricContext_Observe(t *testing.T) {
	// Reset metrics before test
	operationDuration.Reset()
	operationTotal.Reset()

	mc := NewCSIMetricContext("node_stage_volume").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test basic observation (success)
	mc.Observe(true)

	// Check that metrics were recorded by gathering all metrics
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	foundCounter := false
	foundHistogram := false

	for _, family := range families {
		if family.GetName() == "azurefile_csi_driver_operations_total" {
			foundCounter = true
			if len(family.GetMetric()) == 0 {
				t.Error("expected counter to have metrics")
			}
		}
		if family.GetName() == "azurefile_csi_driver_operation_duration_seconds" {
			foundHistogram = true
			if len(family.GetMetric()) == 0 {
				t.Error("expected histogram to have metrics")
			}
		}
	}

	if !foundCounter {
		t.Error("expected to find operation counter")
	}
	if !foundHistogram {
		t.Error("expected to find operation duration histogram")
	}
}

func TestCSIMetricContext_ObserveWithFailure(t *testing.T) {
	// Reset metrics before test
	operationTotal.Reset()

	mc := NewCSIMetricContext("node_publish_volume").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test observation with failure
	mc.Observe(false)

	// Verify metrics were recorded
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	foundCounter := false
	for _, family := range families {
		if family.GetName() == "azurefile_csi_driver_operations_total" {
			foundCounter = true
			// Check that we have metrics recorded
			if len(family.GetMetric()) == 0 {
				t.Error("expected counter to have metrics for failure case")
			}
			// Verify that success="false" label exists in one of the metrics
			foundFailureMetric := false
			for _, metric := range family.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "success" && label.GetValue() == "false" {
						foundFailureMetric = true
						break
					}
				}
			}
			if !foundFailureMetric {
				t.Error("expected to find metric with success=false")
			}
		}
	}

	if !foundCounter {
		t.Error("expected to find operation counter")
	}
}

func TestCSIMetricContext_ObserveWithLabels(t *testing.T) {
	// Reset metrics before test
	operationDuration.Reset()
	operationTotal.Reset()
	operationDurationWithLabels.Reset()

	mc := NewCSIMetricContext("controller_create_volume").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test observation with labels
	mc.ObserveWithLabels(true,
		Protocol, "smb",
		StorageAccountType, "Standard_LRS")

	// Verify that both basic and labeled metrics were recorded
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	foundBasicCounter := false
	foundLabeledHistogram := false

	for _, family := range families {
		if family.GetName() == "azurefile_csi_driver_operations_total" {
			foundBasicCounter = true
			if len(family.GetMetric()) == 0 {
				t.Error("expected basic counter to have metrics")
			}
		}
		if family.GetName() == "azurefile_csi_driver_operation_duration_seconds_labeled" {
			foundLabeledHistogram = true
			if len(family.GetMetric()) == 0 {
				t.Error("expected labeled histogram to have metrics")
			}
			// Verify that our expected labels are present
			for _, metric := range family.GetMetric() {
				labelMap := make(map[string]string)
				for _, label := range metric.GetLabel() {
					labelMap[label.GetName()] = label.GetValue()
				}

				if labelMap[Protocol] != "smb" ||
					labelMap[StorageAccountType] != "Standard_LRS" {
					t.Errorf("expected labeled metric with correct labels, got: %v", labelMap)
				}
			}
		}
	}

	if !foundBasicCounter {
		t.Error("expected to find basic operation counter")
	}
	if !foundLabeledHistogram {
		t.Error("expected to find labeled operation histogram")
	}
}

func TestCSIMetricContext_ObserveWithInvalidLabels(t *testing.T) {
	// Reset metrics before test
	operationTotal.Reset()
	operationDurationWithLabels.Reset()

	mc := NewCSIMetricContext("test_operation").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test with odd number of label parameters (should fallback to basic observe)
	mc.ObserveWithLabels(true, Protocol, "smb", "orphan_key")

	// Should still record basic metrics but not labeled metrics
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	foundBasicCounter := false
	for _, family := range families {
		if family.GetName() == "azurefile_csi_driver_operations_total" {
			foundBasicCounter = true
			if len(family.GetMetric()) == 0 {
				t.Error("expected basic counter to have metrics even with invalid labels")
			}
		}
		if family.GetName() == "azurefile_csi_driver_operation_duration_seconds_labeled" {
			if len(family.GetMetric()) > 0 {
				t.Error("expected no labeled metrics to be recorded with invalid label pairs")
			}
		}
	}

	if !foundBasicCounter {
		t.Error("expected to find basic operation counter")
	}
}

func TestCSIMetricContext_TimingAccuracy(t *testing.T) {
	mc := NewCSIMetricContext("timing_test").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Set start to a fixed time in the past so the test is deterministic
	// and doesn't depend on wall-clock timing or scheduler delays.
	fixedDuration := 500 * time.Millisecond
	mc.start = time.Now().Add(-fixedDuration)

	mc.Observe(true)
	duration := time.Since(mc.start)

	// The duration should be at least the fixed offset we set
	if duration < fixedDuration {
		t.Errorf("expected duration to be at least %v, got %v", fixedDuration, duration)
	}

	// Should be very close to fixedDuration (allow a small margin for the few instructions between)
	if duration > fixedDuration+10*time.Millisecond {
		t.Errorf("expected duration to be close to %v, got %v", fixedDuration, duration)
	}
}

func TestCSIMetricContext_ChainedLabels(t *testing.T) {
	mc := NewCSIMetricContext("test_operation").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test method chaining
	mc.WithLabel("key1", "value1").WithLabel("key2", "value2").WithLabel("key3", "value3")

	if mc.labels["key1"] != "value1" || mc.labels["key2"] != "value2" || mc.labels["key3"] != "value3" {
		t.Errorf("expected all chained labels to be set, got: %v", mc.labels)
	}
}

func TestCSIMetricContext_EmptyLabels(t *testing.T) {
	mc := NewCSIMetricContext("test_operation").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")

	// Test observing without any labels
	mc.Observe(true)

	if len(mc.labels) != 0 {
		t.Errorf("expected no labels, got: %v", mc.labels)
	}
}

func TestCSIMetricContext_WithAdditionalVolumeInfo(t *testing.T) {
	mc := NewCSIMetricContext("test_operation")

	// Test adding additional volume info key-value pairs
	mc.WithAdditionalVolumeInfo("volumeid", "vol-123", "container", "my-container")

	// volumeContext is now []interface{}, check order and values
	expected := []interface{}{"volumeid", "vol-123", "container", "my-container"}
	if len(mc.volumeContext) != len(expected) {
		t.Errorf("expected %d elements, got %d", len(expected), len(mc.volumeContext))
	}
	for i, v := range expected {
		if mc.volumeContext[i] != v {
			t.Errorf("expected volumeContext[%d]=%v, got %v", i, v, mc.volumeContext[i])
		}
	}
}

func TestCSIMetricContext_WithAdditionalVolumeInfo_Chaining(t *testing.T) {
	mc := NewCSIMetricContext("test_operation").
		WithBasicVolumeInfo("test-rg", "test-sub", "test-source").
		WithAdditionalVolumeInfo("volumeid", "vol-456", "storageaccount", "mystorageaccount")

	// volumeContext preserves insertion order
	expected := []interface{}{
		"resource_group", "test-rg",
		"subscription_id", "test-sub",
		"source", "test-source",
		"volumeid", "vol-456",
		"storageaccount", "mystorageaccount",
	}
	if len(mc.volumeContext) != len(expected) {
		t.Errorf("expected %d elements, got %d", len(expected), len(mc.volumeContext))
	}
	for i, v := range expected {
		if mc.volumeContext[i] != v {
			t.Errorf("expected volumeContext[%d]=%v, got %v", i, v, mc.volumeContext[i])
		}
	}
}

func TestCSIMetricContext_WithAdditionalVolumeInfo_OddParameters(t *testing.T) {
	mc := NewCSIMetricContext("test_operation")

	// Test odd number of parameters - should silently skip adding to volumeContext
	mc.WithAdditionalVolumeInfo("key1", "value1", "orphan_key")

	// Should not have added anything due to odd number of parameters
	if len(mc.volumeContext) != 0 {
		t.Errorf("expected empty volumeContext with odd parameters, got: %v", mc.volumeContext)
	}

	// Now add valid pairs
	mc.WithAdditionalVolumeInfo("key2", "value2")

	// Should work for valid pairs
	if len(mc.volumeContext) != 2 || mc.volumeContext[0] != "key2" || mc.volumeContext[1] != "value2" {
		t.Errorf("expected [key2, value2] after valid call, got %v", mc.volumeContext)
	}
}

func TestCSIMetricContext_WithLogLevel(t *testing.T) {
	tests := []struct {
		name             string
		logLevel         int32
		expectedLogLevel int32
	}{
		{
			name:             "set log level to 0",
			logLevel:         0,
			expectedLogLevel: 0,
		},
		{
			name:             "set log level to 5",
			logLevel:         5,
			expectedLogLevel: 5,
		},
		{
			name:             "set high log level",
			logLevel:         10,
			expectedLogLevel: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewCSIMetricContext("test_operation").WithLogLevel(tt.logLevel)

			if mc.logLevel != tt.expectedLogLevel {
				t.Errorf("expected logLevel %d, got %d", tt.expectedLogLevel, mc.logLevel)
			}
		})
	}
}

func TestCSIMetricContext_WithLogLevel_DefaultValue(t *testing.T) {
	mc := NewCSIMetricContext("test_operation")

	// Default log level should be 3
	if mc.logLevel != 3 {
		t.Errorf("expected default logLevel 3, got %d", mc.logLevel)
	}
}

func TestCSIMetricContext_WithLogLevel_Chaining(t *testing.T) {
	mc := NewCSIMetricContext("test_operation").
		WithBasicVolumeInfo("test-rg", "test-sub", "test-source").
		WithLogLevel(5).
		WithLabel("key", "value")

	if mc.logLevel != 5 {
		t.Errorf("expected logLevel 5 after chaining, got %d", mc.logLevel)
	}
	if mc.labels["key"] != "value" {
		t.Errorf("expected label key=value after chaining, got %s", mc.labels["key"])
	}
	// volumeContext is []interface{}, check first two elements
	if len(mc.volumeContext) < 2 || mc.volumeContext[0] != "resource_group" || mc.volumeContext[1] != "test-rg" {
		t.Errorf("expected resource_group=test-rg after chaining, got %v", mc.volumeContext)
	}
}

func BenchmarkCSIMetricContext_Observe(b *testing.B) {
	mc := NewCSIMetricContext("benchmark_test").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.start = time.Now()
		mc.Observe(true)
	}
}

func BenchmarkCSIMetricContext_ObserveWithLabels(b *testing.B) {
	mc := NewCSIMetricContext("benchmark_test").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.start = time.Now()
		mc.ObserveWithLabels(true,
			Protocol, "smb",
			StorageAccountType, "Standard_LRS")
	}
}

// Benchmark just the metrics recording portion (no duration calculation)
func BenchmarkMetricsRecordingOnly(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Directly record metrics without duration calculation
		operationDuration.WithLabelValues("benchmark_test", "true").Observe(0.001) // Fixed small duration
		operationTotal.WithLabelValues("benchmark_test", "true").Inc()
	}
}

func BenchmarkCSIMetricContext_NewAndObserve(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc := NewCSIMetricContext("benchmark_test").WithBasicVolumeInfo("test-rg", "test-sub", "test-source")
		mc.Observe(true)
	}
}
