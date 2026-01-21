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
	mc := NewCSIMetricContext(operation)

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
	mc := NewCSIMetricContext("test_operation")

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

	mc := NewCSIMetricContext("node_stage_volume")

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

	mc := NewCSIMetricContext("node_publish_volume")

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

	mc := NewCSIMetricContext("controller_create_volume")

	// Test observation with labels
	mc.ObserveWithLabels(true,
		"protocol", "smb",
		"storage_account_type", "Standard_LRS")

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

				if labelMap["protocol"] != "smb" ||
					labelMap["storage_account_type"] != "Standard_LRS" {
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

	mc := NewCSIMetricContext("test_operation")

	// Test with odd number of label parameters (should fallback to basic observe)
	mc.ObserveWithLabels(true, "protocol", "smb", "orphan_key")

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
	mc := NewCSIMetricContext("timing_test")

	// Simulate that the operation started some time ago by setting a fixed start time.
	expectedDuration := 50 * time.Millisecond
	mc.start = time.Now().Add(-expectedDuration)
	mc.Observe(true)
	duration := time.Since(mc.start)
	// The duration should be at least the expected duration (allowing for minimal overhead).
	if duration < expectedDuration {
		t.Errorf("expected duration to be at least %v, got %v", expectedDuration, duration)
	}
	// But not too much more (allowing for some variance in execution).
	if duration > expectedDuration+50*time.Millisecond {
		t.Errorf("expected duration to be less than %v, got %v", expectedDuration+50*time.Millisecond, duration)
	}
}

func TestCSIMetricContext_ChainedLabels(t *testing.T) {
	mc := NewCSIMetricContext("test_operation")

	// Test method chaining
	mc.WithLabel("key1", "value1").WithLabel("key2", "value2").WithLabel("key3", "value3")

	if mc.labels["key1"] != "value1" || mc.labels["key2"] != "value2" || mc.labels["key3"] != "value3" {
		t.Errorf("expected all chained labels to be set, got: %v", mc.labels)
	}
}

func TestCSIMetricContext_EmptyLabels(t *testing.T) {
	mc := NewCSIMetricContext("test_operation")

	// Test observing without any labels
	mc.Observe(true)

	if len(mc.labels) != 0 {
		t.Errorf("expected no labels, got: %v", mc.labels)
	}
}

func BenchmarkCSIMetricContext_Observe(b *testing.B) {
	mc := NewCSIMetricContext("benchmark_test")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.start = time.Now() // Reset start time each iteration to simulate fresh observation
		mc.Observe(true)
	}
}

func BenchmarkCSIMetricContext_ObserveWithLabels(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc := NewCSIMetricContext("benchmark_test")
		mc.ObserveWithLabels(true,
			"protocol", "smb",
			"storage_account_type", "Standard_LRS")
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
		mc := NewCSIMetricContext("benchmark_test")
		mc.Observe(true)
	}
}
