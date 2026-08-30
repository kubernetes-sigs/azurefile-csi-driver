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

package stats

import (
	"context"
	"errors"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/procfs"
	"k8s.io/component-base/metrics"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile/stats/volume"
)

type fakeVolumeLister struct {
	volumes volume.MetadataList
	err     error
}

func (l fakeVolumeLister) List(context.Context) (volume.MetadataList, error) {
	return l.volumes, l.err
}

func TestVolumeStatsCollector(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{{
			StorageAccountName: "account",
			ShareName:          "share",
		}},
	})
	collector.readCIFS = func(string) ([]CIFSStats, error) {
		return []CIFSStats{{
			Device:       `\\account.file.core.windows.net\share`,
			BytesRead:    100,
			BytesWritten: 200,
			ReadsTotal:   10,
			ReadsFailed:  1,
		}}, nil
	}
	collector.readNFS = func() ([]NFSStats, error) {
		return []NFSStats{{
			Device:     "account.file.core.windows.net:/account/share",
			MountPoint: "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
			MountStats: procfs.MountStatsNFS{
				Bytes: procfs.NFSBytesStats{ReadTotal: 300, WriteTotal: 400},
				Operations: []procfs.NFSOperationStats{{
					Operation: "READ",
					Requests:  30,
					Errors:    3,
				}},
			},
		}}, nil
	}

	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	readBytes := metricFamily(t, families, "azurefile_csi_driver_volume_read_bytes_total")
	if len(readBytes.Metric) != 2 {
		t.Fatalf("read metric count = %d, want 2", len(readBytes.Metric))
	}
	if got := metricValue(t, readBytes, map[string]string{
		"protocol":        "smb",
		"storage_account": "account",
		"file_share":      "share",
	}); got != 100 {
		t.Errorf("SMB read bytes = %v, want 100", got)
	}
	if got := metricValue(t, readBytes, map[string]string{
		"protocol":        "nfs",
		"storage_account": "account",
		"file_share":      "share",
	}); got != 300 {
		t.Errorf("NFS read bytes = %v, want 300", got)
	}

	requests := metricFamily(t, families, "azurefile_csi_driver_volume_operation_requests_total")
	if got := metricValue(t, requests, map[string]string{"protocol": "nfs", "operation": "read"}); got != 30 {
		t.Errorf("NFS read requests = %v, want 30", got)
	}

	up := metricFamily(t, families, "azurefile_csi_driver_volume_stats_collector_up")
	for _, source := range []string{"persistentvolumes", "smb", "nfs"} {
		if got := metricValue(t, up, map[string]string{"source": source}); got != 1 {
			t.Errorf("%s collector up = %v, want 1", source, got)
		}
	}
}

func TestVolumeStatsCollectorContinuesWhenCIFSFails(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{{
			StorageAccountName: "account",
			ShareName:          "share",
		}},
	})
	collector.readCIFS = func(string) ([]CIFSStats, error) {
		return nil, errors.New("CIFS unavailable")
	}
	collector.readNFS = func() ([]NFSStats, error) {
		return []NFSStats{{
			Device:     "account.file.core.windows.net:/account/share",
			MountPoint: "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
			MountStats: procfs.MountStatsNFS{
				Bytes: procfs.NFSBytesStats{ReadTotal: 300},
			},
		}}, nil
	}

	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	readBytes := metricFamily(t, families, "azurefile_csi_driver_volume_read_bytes_total")
	if len(readBytes.Metric) != 1 {
		t.Fatalf("read metric count = %d, want 1", len(readBytes.Metric))
	}
	up := metricFamily(t, families, "azurefile_csi_driver_volume_stats_collector_up")
	if got := metricValue(t, up, map[string]string{"source": "smb"}); got != 0 {
		t.Errorf("SMB collector up = %v, want 0", got)
	}
	if got := metricValue(t, up, map[string]string{"source": "nfs"}); got != 1 {
		t.Errorf("NFS collector up = %v, want 1", got)
	}
}

func TestVolumeStatsCollectorMetricsEndpoint(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{{
			StorageAccountName: "account",
			ShareName:          "share",
		}},
	})
	collector.readCIFS = func(string) ([]CIFSStats, error) {
		return []CIFSStats{{
			Device:       `\\account.file.core.windows.net\share`,
			BytesRead:    123,
			BytesWritten: 456,
			ReadsTotal:   7,
			ReadsFailed:  2,
		}}, nil
	}
	collector.readNFS = func() ([]NFSStats, error) {
		return nil, nil
	}

	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("metrics status = %d, want 200: %s", response.Code, response.Body.String())
	}

	body := response.Body.String()
	if strings.Contains(body, "mountpoint=") {
		t.Fatalf("metrics unexpectedly contain mountpoint label:\n%s", body)
	}
	if strings.Contains(body, "target=") {
		t.Fatalf("metrics unexpectedly contain target label:\n%s", body)
	}
	if !strings.Contains(body, `storage_account="account"`) ||
		!strings.Contains(body, `file_share="share"`) {
		t.Fatalf("metrics do not contain storage account and file share labels:\n%s", body)
	}
	assertMetricLine(t, body, "azurefile_csi_driver_volume_read_bytes_total", "smb", 123)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_written_bytes_total", "smb", 456)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_operation_requests_total", "smb", 7)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_operation_errors_total", "smb", 2)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_stats_collector_up", "persistentvolumes", 1)
}

func assertMetricLine(t *testing.T, body, name, labelValue string, value float64) {
	t.Helper()
	pattern := `(?m)^` + regexp.QuoteMeta(name) +
		`\{[^\n]*="` + regexp.QuoteMeta(labelValue) +
		`"[^\n]*\} ` + regexp.QuoteMeta(metricTextValue(value)) + `$`
	if !regexp.MustCompile(pattern).MatchString(body) {
		t.Errorf("metric %s with label value %q and value %v not found in:\n%s", name, labelValue, value, body)
	}
}

func metricTextValue(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func metricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func metricValue(t *testing.T, family *dto.MetricFamily, labels map[string]string) float64 {
	t.Helper()
	for _, metric := range family.Metric {
		matched := true
		for name, value := range labels {
			found := false
			for _, label := range metric.Label {
				if label.GetName() == name && label.GetValue() == value {
					found = true
					break
				}
			}
			if !found {
				matched = false
				break
			}
		}
		if matched {
			if metric.Counter != nil {
				return metric.Counter.GetValue()
			}
			return metric.Gauge.GetValue()
		}
	}
	t.Fatalf("metric with labels %v not found in %q", labels, family.GetName())
	return 0
}
