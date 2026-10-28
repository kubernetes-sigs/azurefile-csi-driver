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
	"fmt"

	"github.com/prometheus/procfs"
)

// NFSStats contains one NFS client mount and the statistics reported for it in
// /proc/self/mountstats.
type NFSStats struct {
	// Device identifies the remote NFS server and exported path.
	Device string
	// MountPoint is the local path at which the NFS filesystem is mounted.
	MountPoint string
	// FilesystemType is the mount's NFS filesystem type, normally nfs or nfs4.
	FilesystemType string
	// MountStats contains byte, event, operation, and RPC transport statistics.
	MountStats procfs.MountStatsNFS
}

// ProcessNFSStats reads NFS client statistics from /proc/self/mountstats.
// Every NFS mount is returned, including bind mounts which may expose the same
// underlying counters through different mount points.
func ProcessNFSStats() ([]NFSStats, error) {
	proc, err := procfs.Self()
	if err != nil {
		return nil, fmt.Errorf("failed to open self procfs entry: %w", err)
	}

	return processNFSStats(proc)
}

func processNFSStats(proc procfs.Proc) ([]NFSStats, error) {
	mounts, err := proc.MountStats()
	if err != nil {
		return nil, fmt.Errorf("failed to read NFS mount stats: %w", err)
	}

	stats := make([]NFSStats, 0)
	for _, mount := range mounts {
		if mount.Type != "nfs" && mount.Type != "nfs4" {
			continue
		}

		mountStats, ok := mount.Stats.(*procfs.MountStatsNFS)
		if !ok || mountStats == nil {
			continue
		}

		stats = append(stats, NFSStats{
			Device:         mount.Device,
			MountPoint:     mount.Mount,
			FilesystemType: mount.Type,
			MountStats:     *mountStats,
		})
	}

	return stats, nil
}
