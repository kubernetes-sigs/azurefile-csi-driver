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
	"time"

	"k8s.io/component-base/metrics"
	"k8s.io/klog/v2"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile/stats/volume"
)

const collectionTimeout = 10 * time.Second

var filesystemLabels = []string{"protocol", "storage_account", "file_share"}

// VolumeStatsCollector exports kernel filesystem counters for Azure File CSI
// persistent volumes through the driver's existing metrics registry.
type VolumeStatsCollector struct {
	metrics.BaseStableCollector

	volumes    volume.MetadataLister
	cifsPath   string
	readCIFS   func(string) ([]CIFSStats, error)
	readNFS    func() ([]NFSStats, error)
	readBytes  *metrics.Desc
	writeBytes *metrics.Desc
	requests   *metrics.Desc
	errors     *metrics.Desc
	up         *metrics.Desc
}

// NewVolumeStatsCollector creates a collector that discovers Azure File PVs
// at scrape time and filters CIFS and NFS kernel statistics against them.
func NewVolumeStatsCollector(volumes volume.MetadataLister) *VolumeStatsCollector {
	return &VolumeStatsCollector{
		volumes:  volumes,
		cifsPath: CIFSStatsPath,
		readCIFS: ProcessCIFSStats,
		readNFS:  ProcessNFSStats,
		readBytes: metrics.NewDesc(
			"azurefile_csi_driver_volume_read_bytes_total",
			"Total bytes read from the remote filesystem.",
			filesystemLabels, nil, metrics.ALPHA, "",
		),
		writeBytes: metrics.NewDesc(
			"azurefile_csi_driver_volume_written_bytes_total",
			"Total bytes written to the remote filesystem.",
			filesystemLabels, nil, metrics.ALPHA, "",
		),
		requests: metrics.NewDesc(
			"azurefile_csi_driver_volume_operation_requests_total",
			"Total remote filesystem operation requests.",
			append(filesystemLabels, "operation"), nil, metrics.ALPHA, "",
		),
		errors: metrics.NewDesc(
			"azurefile_csi_driver_volume_operation_errors_total",
			"Total failed remote filesystem operations.",
			append(filesystemLabels, "operation"), nil, metrics.ALPHA, "",
		),
		up: metrics.NewDesc(
			"azurefile_csi_driver_volume_stats_collector_up",
			"Whether the collector successfully read a statistics source.",
			[]string{"source"}, nil, metrics.ALPHA, "",
		),
	}
}

func (c *VolumeStatsCollector) DescribeWithStability(ch chan<- *metrics.Desc) {
	ch <- c.readBytes
	ch <- c.writeBytes
	ch <- c.requests
	ch <- c.errors
	ch <- c.up
}

func (c *VolumeStatsCollector) CollectWithStability(ch chan<- metrics.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()

	volumes, err := c.volumes.List(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to discover Azure File persistent volumes for filesystem metrics")
		c.emitUp(ch, "persistentvolumes", false)
		c.emitUp(ch, string(ProtocolSMB), false)
		c.emitUp(ch, string(ProtocolNFS), false)
		return
	}
	c.emitUp(ch, "persistentvolumes", true)

	var cifsStats []CIFSStats
	if cifsStats, err = c.readCIFS(c.cifsPath); err != nil {
		klog.ErrorS(err, "Failed to read CIFS filesystem statistics")
		c.emitUp(ch, string(ProtocolSMB), false)
	} else {
		c.emitUp(ch, string(ProtocolSMB), true)
	}

	var nfsStats []NFSStats
	if nfsStats, err = c.readNFS(); err != nil {
		klog.ErrorS(err, "Failed to read NFS filesystem statistics")
		c.emitUp(ch, string(ProtocolNFS), false)
	} else {
		c.emitUp(ch, string(ProtocolNFS), true)
	}

	collection := NewStatsCollection(volumes, cifsStats, nfsStats)
	for _, filesystem := range collection.Filesystems {
		labels := []string{
			string(filesystem.Protocol),
			filesystem.StorageAccount,
			filesystem.FileShare,
		}
		ch <- metrics.NewLazyConstMetric(c.readBytes, metrics.CounterValue, float64(filesystem.BytesRead), labels...)
		ch <- metrics.NewLazyConstMetric(c.writeBytes, metrics.CounterValue, float64(filesystem.BytesWritten), labels...)
		for operation, operationStats := range filesystem.Operations {
			operationLabels := append(labels, operation)
			ch <- metrics.NewLazyConstMetric(c.requests, metrics.CounterValue, float64(operationStats.Requests), operationLabels...)
			ch <- metrics.NewLazyConstMetric(c.errors, metrics.CounterValue, float64(operationStats.Errors), operationLabels...)
		}
	}
}

func (c *VolumeStatsCollector) emitUp(ch chan<- metrics.Metric, source string, up bool) {
	value := 0.0
	if up {
		value = 1
	}
	ch <- metrics.NewLazyConstMetric(c.up, metrics.GaugeValue, value, source)
}
