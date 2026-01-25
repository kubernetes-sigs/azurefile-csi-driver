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
	"time"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	subSystem = "azurefile_csi_driver"
)

var (
	operationDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      subSystem,
			Name:           "operation_duration_seconds",
			Help:           "Histogram of CSI operation duration in seconds",
			Buckets:        []float64{0.1, 0.2, 0.5, 1, 5, 10, 15, 20, 30, 40, 50, 60, 100, 200, 300},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"operation", "success"},
	)

	operationDurationWithLabels = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      subSystem,
			Name:           "operation_duration_seconds_labeled",
			Help:           "Histogram of CSI operation duration with additional labels",
			Buckets:        []float64{0.1, 0.2, 0.5, 1, 5, 10, 15, 20, 30, 40, 50, 60, 100, 200, 300},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"operation", "success", "protocol", "storage_account_type"},
	)

	operationTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      subSystem,
			Name:           "operations_total",
			Help:           "Total number of CSI operations",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"operation", "success"},
	)
)

func init() {
	legacyregistry.MustRegister(operationDuration)
	legacyregistry.MustRegister(operationDurationWithLabels)
	legacyregistry.MustRegister(operationTotal)
}

// CSIMetricContext represents the context for CSI operation metrics
type CSIMetricContext struct {
	operation string
	start     time.Time
	labels    map[string]string
}

// NewCSIMetricContext creates a new CSI metric context
func NewCSIMetricContext(operation string) *CSIMetricContext {
	return &CSIMetricContext{
		operation: operation,
		start:     time.Now(),
		labels:    make(map[string]string),
	}
}

// WithLabel adds a label to the metric context
func (mc *CSIMetricContext) WithLabel(key, value string) *CSIMetricContext {
	if mc.labels == nil {
		mc.labels = make(map[string]string)
	}
	mc.labels[key] = value
	return mc
}

// Observe records the operation result and duration
func (mc *CSIMetricContext) Observe(success bool) {
	duration := time.Since(mc.start).Seconds()
	successStr := "false"
	if success {
		successStr = "true"
	}

	// Always record basic metrics
	operationDuration.WithLabelValues(mc.operation, successStr).Observe(duration)
	operationTotal.WithLabelValues(mc.operation, successStr).Inc()

	// Record detailed metrics if labels are present
	if len(mc.labels) > 0 {
		protocol := mc.labels["protocol"]
		storageAccountType := mc.labels["storage_account_type"]

		operationDurationWithLabels.WithLabelValues(
			mc.operation,
			successStr,
			protocol,
			storageAccountType,
		).Observe(duration)
	}
}

// ObserveWithLabels records the operation with provided label pairs
func (mc *CSIMetricContext) ObserveWithLabels(success bool, labelPairs ...string) {
	if len(labelPairs)%2 != 0 {
		// Invalid label pairs, just observe without labels
		mc.Observe(success)
		return
	}

	for i := 0; i < len(labelPairs); i += 2 {
		mc.WithLabel(labelPairs[i], labelPairs[i+1])
	}
	mc.Observe(success)
}
