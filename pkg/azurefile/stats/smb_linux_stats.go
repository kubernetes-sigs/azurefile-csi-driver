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
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

// CIFSStatsPath is the Linux CIFS client's statistics file.
const CIFSStatsPath = "/proc/fs/cifs/Stats"

/*

cat /proc/fs/cifs/Stats will looks like below

Max requests in flight: 582
1) \\f389ac36a70ed4cb5bc601e.file.core.windows.net\pvc-100d6de7-1305-4216-ad39-8788b96a409c
SMBs: 7352 since 2026-08-26 10:31:19 UTC
Bytes read: 18927616  Bytes written: 1080492032
Open files: 1 total (local), 1 open on server
TreeConnects: 1 total 0 failed
TreeDisconnects: 0 total 0 failed
Creates: 18 total 0 failed
Closes: 17 total 0 failed
Flushes: 3 total 0 failed
Reads: 4622 total 0 failed
Writes: 2668 total 0 failed
Locks: 0 total 0 failed
IOCTLs: 1 total 1 failed
QueryDirectories: 5 total 0 failed
ChangeNotifies: 0 total 0 failed
QueryInfos: 12 total 0 failed
SetInfos: 5 total 0 failed
OplockBreaks: 0 sent 0 failed

*/

// CIFSStats contains the per-tree-connection counters reported by the Linux
// SMB client in /proc/fs/cifs/Stats. The operation counters correspond to SMB2
// command codes recorded in the tree connection's sent and failed arrays.
type CIFSStats struct {
	// Device is the UNC path identifying the SMB server and share.
	Device string

	// BytesRead is the number of file-data bytes received from the share.
	BytesRead uint64
	// BytesWritten is the number of file-data bytes sent to the share.
	BytesWritten uint64

	// OpenFilesLocal is the number of file handles tracked locally for the share.
	OpenFilesLocal uint64
	// OpenFilesServer is the number of file handles currently open on the server.
	OpenFilesServer uint64

	// TreeConnectTotal is the number of SMB TREE_CONNECT requests sent.
	TreeConnectTotal uint64
	// TreeConnectFailed is the number of failed SMB TREE_CONNECT requests.
	TreeConnectFailed uint64
	// TreeDisconnectTotal is the number of SMB TREE_DISCONNECT requests sent.
	TreeDisconnectTotal uint64
	// TreeDisconnectFailed is the number of failed SMB TREE_DISCONNECT requests.
	TreeDisconnectFailed uint64

	// CreatesTotal is the number of SMB CREATE requests used to open or create
	// files and directories.
	CreatesTotal uint64
	// CreatesFailed is the number of failed SMB CREATE requests.
	CreatesFailed uint64
	// ClosesTotal is the number of SMB CLOSE requests sent to release handles.
	ClosesTotal uint64
	// ClosesFailed is the number of failed SMB CLOSE requests.
	ClosesFailed uint64
	// FlushesTotal is the number of SMB FLUSH requests sent.
	FlushesTotal uint64
	// FlushesFailed is the number of failed SMB FLUSH requests.
	FlushesFailed uint64

	// ReadsTotal is the number of SMB READ requests sent, not the number of bytes read.
	ReadsTotal uint64
	// ReadsFailed is the number of failed SMB READ requests.
	ReadsFailed uint64
	// WritesTotal is the number of SMB WRITE requests sent, not the number of bytes written.
	WritesTotal uint64
	// WritesFailed is the number of failed SMB WRITE requests.
	WritesFailed uint64
	// LocksTotal is the number of SMB LOCK requests sent for locking or unlocking ranges.
	LocksTotal uint64
	// LocksFailed is the number of failed SMB LOCK requests.
	LocksFailed uint64
	// IOCTLSTotal is the number of SMB IOCTL requests sent.
	IOCTLSTotal uint64
	// IOCTLSFailed is the number of failed SMB IOCTL requests.
	IOCTLSFailed uint64

	// QueryDirectoriesTotal is the number of SMB QUERY_DIRECTORY requests sent.
	QueryDirectoriesTotal uint64
	// QueryDirectoriesFailed is the number of failed SMB QUERY_DIRECTORY requests.
	QueryDirectoriesFailed uint64
	// ChangeNotifiesTotal is the number of SMB CHANGE_NOTIFY requests sent.
	ChangeNotifiesTotal uint64
	// ChangeNotifiesFailed is the number of failed SMB CHANGE_NOTIFY requests.
	ChangeNotifiesFailed uint64
	// QueryInfosTotal is the number of SMB QUERY_INFO requests sent.
	QueryInfosTotal uint64
	// QueryInfosFailed is the number of failed SMB QUERY_INFO requests.
	QueryInfosFailed uint64
	// SetInfosTotal is the number of SMB SET_INFO requests sent.
	SetInfosTotal uint64
	// SetInfosFailed is the number of failed SMB SET_INFO requests.
	SetInfosFailed uint64

	// OplockBreaksSent is the number of SMB OPLOCK_BREAK messages sent.
	OplockBreaksSent uint64
	// OplockBreaksFailed is the number of failed SMB OPLOCK_BREAK messages.
	OplockBreaksFailed uint64
}

func ProcessCIFSStats(cifsStatsPath string) ([]CIFSStats, error) {
	if strings.TrimSpace(cifsStatsPath) == "" {
		return nil, fmt.Errorf("cifsStatsPath is empty")
	}

	file, err := os.Open(cifsStatsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cifs stats file %s: %v", cifsStatsPath, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			klog.Warningf("failed to close cifs stats file %s: %v", cifsStatsPath, cerr)
		}
	}()

	stats := make([]CIFSStats, 0)
	var current *CIFSStats

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if Device, ok := parseShareLine(line); ok {
			if current != nil {
				stats = append(stats, *current)
			}
			current = &CIFSStats{Device: Device}
			continue
		}

		// Global and transport-level fields occur outside and between share
		// records. They cannot be attributed to an individual share.
		if current == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "Bytes read:"):
			if _, err := fmt.Sscanf(line, "Bytes read: %d Bytes written: %d", &current.BytesRead, &current.BytesWritten); err != nil {
				return nil, parseError(cifsStatsPath, lineNumber, line, err)
			}
		case strings.HasPrefix(line, "Open files:"):
			if _, err := fmt.Sscanf(line, "Open files: %d total (local), %d open on server", &current.OpenFilesLocal, &current.OpenFilesServer); err != nil {
				return nil, parseError(cifsStatsPath, lineNumber, line, err)
			}
		default:
			total, failed, operation, matched, err := parseOperationLine(line)
			if err != nil {
				return nil, parseError(cifsStatsPath, lineNumber, line, err)
			}
			if !matched {
				continue
			}

			switch operation {
			case "TreeConnects":
				current.TreeConnectTotal, current.TreeConnectFailed = total, failed
			case "TreeDisconnects":
				current.TreeDisconnectTotal, current.TreeDisconnectFailed = total, failed
			case "Creates":
				current.CreatesTotal, current.CreatesFailed = total, failed
			case "Closes":
				current.ClosesTotal, current.ClosesFailed = total, failed
			case "Flushes":
				current.FlushesTotal, current.FlushesFailed = total, failed
			case "Reads":
				current.ReadsTotal, current.ReadsFailed = total, failed
			case "Writes":
				current.WritesTotal, current.WritesFailed = total, failed
			case "Locks":
				current.LocksTotal, current.LocksFailed = total, failed
			case "IOCTLs":
				current.IOCTLSTotal, current.IOCTLSFailed = total, failed
			case "QueryDirectories":
				current.QueryDirectoriesTotal, current.QueryDirectoriesFailed = total, failed
			case "ChangeNotifies":
				current.ChangeNotifiesTotal, current.ChangeNotifiesFailed = total, failed
			case "QueryInfos":
				current.QueryInfosTotal, current.QueryInfosFailed = total, failed
			case "SetInfos":
				current.SetInfosTotal, current.SetInfosFailed = total, failed
			case "OplockBreaks":
				current.OplockBreaksSent, current.OplockBreaksFailed = total, failed
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read cifs stats file %s: %w", cifsStatsPath, err)
	}
	if current != nil {
		stats = append(stats, *current)
	}

	return stats, nil
}

func parseShareLine(line string) (string, bool) {
	separator := strings.IndexByte(line, ')')
	if separator <= 0 {
		return "", false
	}
	if _, err := strconv.ParseUint(line[:separator], 10, 64); err != nil {
		return "", false
	}

	Device := strings.TrimSpace(line[separator+1:])
	return Device, Device != ""
}

func parseOperationLine(line string) (uint64, uint64, string, bool, error) {
	separator := strings.IndexByte(line, ':')
	if separator <= 0 {
		return 0, 0, "", false, nil
	}

	operation := line[:separator]
	totalLabel := "total"
	if operation == "OplockBreaks" {
		totalLabel = "sent"
	}

	switch operation {
	case "TreeConnects", "TreeDisconnects", "Creates", "Closes", "Flushes",
		"Reads", "Writes", "Locks", "IOCTLs", "QueryDirectories",
		"ChangeNotifies", "QueryInfos", "SetInfos", "OplockBreaks":
	default:
		return 0, 0, "", false, nil
	}

	fields := strings.Fields(line[separator+1:])
	if len(fields) < 4 || fields[1] != totalLabel || fields[3] != "failed" {
		return 0, 0, operation, true, fmt.Errorf("expected %q operation format", totalLabel)
	}

	total, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, 0, operation, true, fmt.Errorf("invalid %s count %q: %w", totalLabel, fields[0], err)
	}
	failed, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return 0, 0, operation, true, fmt.Errorf("invalid failed count %q: %w", fields[2], err)
	}
	return total, failed, operation, true, nil
}

func parseError(path string, lineNumber int, line string, err error) error {
	return fmt.Errorf("failed to parse cifs stats file %s at line %d (%q): %w", path, lineNumber, line, err)
}
