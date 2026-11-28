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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/procfs"
	"k8s.io/component-base/metrics"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile/stats/volume"
)

type fakeVolumeLister struct {
	volumes volume.MetadataList
	err     error
	list    func(context.Context) (volume.MetadataList, error)
}

func (l fakeVolumeLister) List(ctx context.Context) (volume.MetadataList, error) {
	if l.list != nil {
		return l.list(ctx)
	}
	return l.volumes, l.err
}

func TestVolumeStatsCollectorDiscoveryDeadline(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		list: func(ctx context.Context) (volume.MetadataList, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("volume discovery context has no deadline")
			}
			if remaining := time.Until(deadline); remaining <= 0 || remaining > volumeDiscoveryTimeout {
				t.Errorf("discovery deadline remaining = %v, want between 0 and %v", remaining, volumeDiscoveryTimeout)
			}
			return nil, context.DeadlineExceeded
		},
	})
	collector.readCIFS = func(string) ([]CIFSStats, error) {
		t.Error("CIFS stats read after discovery timed out")
		return nil, nil
	}
	collector.readNFS = func() ([]NFSStats, error) {
		t.Error("NFS stats read after discovery timed out")
		return nil, nil
	}
	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	up := metricFamily(t, families, "azurefile_csi_driver_volume_stats_collector_up")
	for _, source := range []string{"persistentvolumes", "smb", "nfs"} {
		if got := metricValue(t, up, map[string]string{"source": source}); got != 0 {
			t.Errorf("%s collector up = %v, want 0", source, got)
		}
	}
}

func TestVolumeStatsCollector(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{
			{
				Protocol:           volume.ProtocolSMB,
				StorageAccountName: "account",
				ShareName:          "share",
				Server:             "account.file.core.windows.net",
			},
			{
				Protocol:           volume.ProtocolNFS,
				StorageAccountName: "account",
				ShareName:          "share",
				MountPoint:         "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
			},
		},
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

	readBytes := metricFamily(t, families, "azurefile_csi_driver_volume_read_bytes")
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

	requests := metricFamily(t, families, "azurefile_csi_driver_volume_operation_requests")
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

func TestVolumeStatsCollectorSMBIPEndpoints(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{
			{Protocol: volume.ProtocolSMB, Server: "10.10.24.244", StorageAccountName: "firstaccount", ShareName: "share"},
			{Protocol: volume.ProtocolSMB, Server: "10.20.30.40", StorageAccountName: "secondaccount", ShareName: "share"},
		},
	})
	collector.cifsPath = writeCIFSStatsFile(t, "1) \\\\10.10.24.244\\share\n"+
		"Bytes read: 100 Bytes written: 200\nReads: 10 total 2 failed\n"+
		"2) \\\\10.20.30.40\\share\n"+
		"Bytes read: 300 Bytes written: 400\nReads: 30 total 4 failed\n"+
		"3) \\\\10.99.99.99\\share\n"+
		"Bytes read: 500 Bytes written: 600\nReads: 50 total 6 failed\n")
	collector.readNFS = func() ([]NFSStats, error) { return nil, nil }

	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	readBytes := metricFamily(t, families, "azurefile_csi_driver_volume_read_bytes")
	if len(readBytes.Metric) != 2 {
		t.Fatalf("read metric count = %d, want 2", len(readBytes.Metric))
	}
	requests := metricFamily(t, families, "azurefile_csi_driver_volume_operation_requests")
	failures := metricFamily(t, families, "azurefile_csi_driver_volume_operation_errors")
	for _, test := range []struct {
		account  string
		bytes    float64
		requests float64
		errors   float64
	}{
		{account: "firstaccount", bytes: 100, requests: 10, errors: 2},
		{account: "secondaccount", bytes: 300, requests: 30, errors: 4},
	} {
		labels := map[string]string{"protocol": "smb", "storage_account": test.account, "file_share": "share"}
		if got := metricValue(t, readBytes, labels); got != test.bytes {
			t.Errorf("%s read bytes = %v, want %v", test.account, got, test.bytes)
		}
		labels["operation"] = "read"
		if got := metricValue(t, requests, labels); got != test.requests {
			t.Errorf("%s read requests = %v, want %v", test.account, got, test.requests)
		}
		if got := metricValue(t, failures, labels); got != test.errors {
			t.Errorf("%s read errors = %v, want %v", test.account, got, test.errors)
		}
	}
}

func TestVolumeStatsCollectorSourceLifecycles(t *testing.T) {
	for _, protocol := range []string{volume.ProtocolSMB, volume.ProtocolNFS} {
		t.Run(protocol, func(t *testing.T) {
			collector := NewVolumeStatsCollector(fakeVolumeLister{
				volumes: volume.MetadataList{
					{
						Protocol: protocol, StorageAccountName: "account", ShareName: "share",
						Server: "account.file.core.windows.net", MountPoint: "/first", FilesystemID: "0:100",
					},
					{
						Protocol: protocol, StorageAccountName: "account", ShareName: "share",
						Server: "account.file.core.windows.net", MountPoint: "/second", FilesystemID: "0:200",
					},
				},
			})
			var sources []int64
			collector.readCIFS = func(string) ([]CIFSStats, error) {
				var stats []CIFSStats
				if protocol == volume.ProtocolSMB {
					for _, source := range sources {
						if source < 0 {
							continue
						}
						value := uint64(source)
						stats = append(stats, CIFSStats{
							Device:    `\\account.file.core.windows.net\share`,
							BytesRead: value, BytesWritten: value * 2,
							ReadsTotal: value / 10, ReadsFailed: value / 100,
						})
					}
				}
				return stats, nil
			}
			collector.readNFS = func() ([]NFSStats, error) {
				var stats []NFSStats
				if protocol == volume.ProtocolNFS {
					for i, source := range sources {
						if source < 0 {
							continue
						}
						value := uint64(source)
						stats = append(stats, NFSStats{
							Device:     "account.file.core.windows.net:/account/share",
							MountPoint: []string{"/first", "/second"}[i],
							MountStats: procfs.MountStatsNFS{
								Bytes: procfs.NFSBytesStats{ReadTotal: value, WriteTotal: value * 2},
								Operations: []procfs.NFSOperationStats{{
									Operation: "READ", Requests: value / 10, Errors: value / 100,
								}},
							},
						})
					}
				}
				return stats, nil
			}
			registry := metrics.NewKubeRegistry()
			registry.CustomMustRegister(collector)

			for _, step := range []struct {
				name    string
				sources []int64
				total   float64
			}{
				{name: "independent sources", sources: []int64{100, 200}, total: 300},
				{name: "first source removed", sources: []int64{-1, 200}, total: 200},
				{name: "first source returns", sources: []int64{100, 200}, total: 300},
				{name: "first source resets", sources: []int64{0, 200}, total: 200},
				{name: "first source grows after reset", sources: []int64{100, 200}, total: 300},
				{name: "second source resets", sources: []int64{100, 0}, total: 100},
			} {
				t.Run(step.name, func(t *testing.T) {
					sources = step.sources
					families, err := registry.Gather()
					if err != nil {
						t.Fatalf("failed to gather metrics: %v", err)
					}
					for _, check := range []struct {
						name      string
						operation string
						want      float64
					}{
						{name: "azurefile_csi_driver_volume_read_bytes", want: step.total},
						{name: "azurefile_csi_driver_volume_written_bytes", want: step.total * 2},
						{name: "azurefile_csi_driver_volume_operation_requests", operation: "read", want: step.total / 10},
						{name: "azurefile_csi_driver_volume_operation_errors", operation: "read", want: step.total / 100},
					} {
						family := metricFamily(t, families, check.name)
						if family.GetType() != dto.MetricType_GAUGE {
							t.Fatalf("%s type = %v, want gauge", check.name, family.GetType())
						}
						labels := map[string]string{"protocol": protocol, "storage_account": "account", "file_share": "share"}
						if check.operation != "" {
							labels["operation"] = check.operation
						}
						if got := metricValue(t, family, labels); got != check.want {
							t.Errorf("%s = %v, want %v", check.name, got, check.want)
						}
					}
				})
			}
		})
	}
}

func TestVolumeStatsCollectorDiscoveryFailure(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{err: errors.New("discovery unavailable")})

	registry := metrics.NewKubeRegistry()
	registry.CustomMustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	up := metricFamily(t, families, "azurefile_csi_driver_volume_stats_collector_up")
	for _, source := range []string{"persistentvolumes", "smb", "nfs"} {
		if got := metricValue(t, up, map[string]string{"source": source}); got != 0 {
			t.Errorf("%s collector up = %v, want 0", source, got)
		}
	}
}

func TestVolumeStatsCollectorCIFSAvailability(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		up   float64
	}{
		{name: "missing file", path: filepath.Join(t.TempDir(), "missing"), up: 0},
		{name: "readable file without shares", path: writeCIFSStatsFile(t, "Resources in use\nCIFS Session: 0\n"), up: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector := NewVolumeStatsCollector(fakeVolumeLister{})
			collector.cifsPath = test.path
			collector.readNFS = func() ([]NFSStats, error) { return nil, nil }
			registry := metrics.NewKubeRegistry()
			registry.CustomMustRegister(collector)

			families, err := registry.Gather()
			if err != nil {
				t.Fatalf("failed to gather metrics: %v", err)
			}
			up := metricFamily(t, families, "azurefile_csi_driver_volume_stats_collector_up")
			for source, want := range map[string]float64{"smb": test.up, "nfs": 1, "persistentvolumes": 1} {
				if got := metricValue(t, up, map[string]string{"source": source}); got != want {
					t.Errorf("%s collector up = %v, want %v", source, got, want)
				}
			}
		})
	}
}

func TestVolumeStatsCollectorContinuesWhenCIFSFails(t *testing.T) {
	collector := NewVolumeStatsCollector(fakeVolumeLister{
		volumes: volume.MetadataList{{
			Protocol:           volume.ProtocolNFS,
			StorageAccountName: "account",
			ShareName:          "share",
			MountPoint:         "/var/lib/kubelet/plugins/kubernetes.io/csi/file.csi.azure.com/id/globalmount",
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

	readBytes := metricFamily(t, families, "azurefile_csi_driver_volume_read_bytes")
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
			Protocol:           volume.ProtocolSMB,
			StorageAccountName: "account",
			ShareName:          "share",
			Server:             "account.file.core.windows.net",
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
	for _, name := range []string{
		"azurefile_csi_driver_volume_read_bytes",
		"azurefile_csi_driver_volume_written_bytes",
		"azurefile_csi_driver_volume_operation_requests",
		"azurefile_csi_driver_volume_operation_errors",
	} {
		if !strings.Contains(body, "# TYPE "+name+" gauge\n") {
			t.Errorf("%s is not exposed as a gauge", name)
		}
		if strings.Contains(body, name+"_total") {
			t.Errorf("obsolete counter %s_total is still exposed", name)
		}
	}
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
	assertMetricLine(t, body, "azurefile_csi_driver_volume_read_bytes", "smb", 123)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_written_bytes", "smb", 456)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_operation_requests", "smb", 7)
	assertMetricLine(t, body, "azurefile_csi_driver_volume_operation_errors", "smb", 2)
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
