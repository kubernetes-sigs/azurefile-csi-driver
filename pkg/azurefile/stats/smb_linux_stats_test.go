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
	"os"
	"path/filepath"
	"testing"
)

func TestProcessCIFSStats(t *testing.T) {
	const input = `Resources in use
CIFS Session: 2
Share (unique mount targets): 4
Operations (MIDs): 1

Max requests in flight: 106
1) \\server.example\share-one
SMBs: 1170 since 2026-08-26 10:31:19 UTC
Bytes read: 376832  Bytes written: 1073741824
Open files: 1 total (local), 1 open on server
TreeConnects: 1 total 0 failed
TreeDisconnects: 2 total 1 failed
Creates: 15 total 0 failed
Closes: 14 total 0 failed
Flushes: 3 total 0 failed
Reads: 93 total 2 failed
Writes: 1024 total 3 failed
Locks: 4 total 1 failed
IOCTLs: 1 total 1 failed
QueryDirectories: 4 total 0 failed
ChangeNotifies: 5 total 1 failed
QueryInfos: 10 total 0 failed
SetInfos: 5 total 0 failed
OplockBreaks: 6 sent 2 failed
2) \\server.example\share-two
Bytes read: 75812864 Bytes written: 1105846272
Open files: 0 total (local), 0 open on server
Reads: 18488 total 0 failed
Writes: 8849 total 0 failed
Max requests in flight: 806
3) \\other-server.example\share-three
Bytes read: 1024 Bytes written: 2048
Reads: 8 total 0 failed
Writes: 4 total 0 failed
`

	path := writeCIFSStatsFile(t, input)
	stats, err := ProcessCIFSStats(path)
	if err != nil {
		t.Fatalf("ProcessCIFSStats() error = %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("ProcessCIFSStats() returned %d records, want 3", len(stats))
	}

	first := stats[0]
	if first.Device != `\\server.example\share-one` {
		t.Errorf("Device = %q, want %q", first.Device, `\\server.example\share-one`)
	}
	if first.BytesRead != 376832 || first.BytesWritten != 1073741824 {
		t.Errorf("bytes = (%d, %d), want (376832, 1073741824)", first.BytesRead, first.BytesWritten)
	}
	if first.OpenFilesLocal != 1 || first.OpenFilesServer != 1 {
		t.Errorf("open files = (%d, %d), want (1, 1)", first.OpenFilesLocal, first.OpenFilesServer)
	}
	if first.ReadsTotal != 93 || first.ReadsFailed != 2 {
		t.Errorf("reads = (%d, %d), want (93, 2)", first.ReadsTotal, first.ReadsFailed)
	}
	if first.WritesTotal != 1024 || first.WritesFailed != 3 {
		t.Errorf("writes = (%d, %d), want (1024, 3)", first.WritesTotal, first.WritesFailed)
	}
	if first.QueryDirectoriesTotal != 4 || first.ChangeNotifiesFailed != 1 ||
		first.QueryInfosTotal != 10 || first.OplockBreaksSent != 6 ||
		first.OplockBreaksFailed != 2 {
		t.Errorf("extended operation fields were not parsed: %+v", first)
	}

	second := stats[1]
	if second.Device != `\\server.example\share-two` ||
		second.BytesRead != 75812864 || second.ReadsTotal != 18488 ||
		second.WritesTotal != 8849 {
		t.Errorf("second record was not parsed correctly: %+v", second)
	}

	third := stats[2]
	if third.Device != `\\other-server.example\share-three` ||
		third.BytesRead != 1024 || third.BytesWritten != 2048 ||
		third.ReadsTotal != 8 || third.WritesTotal != 4 {
		t.Errorf("third record was not parsed correctly: %+v", third)
	}
}

func TestProcessCIFSStatsNoShares(t *testing.T) {
	path := writeCIFSStatsFile(t, "Resources in use\nCIFS Session: 0\n")
	stats, err := ProcessCIFSStats(path)
	if err != nil {
		t.Fatalf("ProcessCIFSStats() error = %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("ProcessCIFSStats() returned %d records, want 0", len(stats))
	}
}

func TestProcessCIFSStatsMalformedKnownField(t *testing.T) {
	path := writeCIFSStatsFile(t, "1) \\\\server\\share\nReads: invalid total 0 failed\n")
	if _, err := ProcessCIFSStats(path); err == nil {
		t.Fatal("ProcessCIFSStats() error = nil, want parse error")
	}
}

func TestProcessCIFSStatsErrors(t *testing.T) {
	if _, err := ProcessCIFSStats(" "); err == nil {
		t.Fatal("ProcessCIFSStats() with empty path error = nil")
	}
	if _, err := ProcessCIFSStats(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("ProcessCIFSStats() with missing file error = nil")
	}
}

func writeCIFSStatsFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Stats")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write test stats: %v", err)
	}
	return path
}
