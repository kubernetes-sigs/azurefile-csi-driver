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

package azurefile

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type ccacheBuilder struct{ bytes.Buffer }

func (b *ccacheBuilder) u16(v uint16) { _ = binary.Write(b, binary.BigEndian, v) }
func (b *ccacheBuilder) u32(v uint32) { _ = binary.Write(b, binary.BigEndian, v) }
func (b *ccacheBuilder) octets(v string) {
	b.u32(uint32(len(v)))
	b.WriteString(v)
}

func (b *ccacheBuilder) principal(components ...string) {
	b.u32(1)
	b.u32(uint32(len(components)))
	b.octets("FILES.AZURE.STORAGE.MICROSOFT.COM")
	for _, c := range components {
		b.octets(c)
	}
}

func testCredential(server string, end uint32, ticket string) []byte {
	b := &ccacheBuilder{}
	b.principal("AzureFileClient@FILES.AZURE.STORAGE.MICROSOFT.COM")
	b.principal("cifs", server)
	b.u16(18)
	b.octets("session-key")
	b.u32(1)
	b.u32(1)
	b.u32(end)
	b.u32(end)
	b.WriteByte(0)
	b.u32(0)
	b.u32(0) // addresses
	b.u32(1) // authdata
	b.u16(1)
	b.octets("ad")
	b.octets(ticket)
	b.octets("")
	return b.Bytes()
}

func testCcachePrefix() []byte {
	b := &ccacheBuilder{}
	b.u16(krb5CcacheVersion4)
	b.u16(4)
	b.WriteString("hdr!")
	b.principal("AzureFileClient@FILES.AZURE.STORAGE.MICROSOFT.COM")
	return b.Bytes()
}

func TestExtractKrb5Ticket(t *testing.T) {
	now := time.Unix(1000, 0)
	prefix := testCcachePrefix()
	expired := testCredential("acct.file.core.windows.net", 900, "expired")
	older := testCredential("acct.file.core.windows.net", 2000, "older")
	newest := testCredential("ACCT.file.core.windows.net", 3000, "newest")
	other := testCredential("other.file.core.windows.net", 4000, "other")
	cache := bytes.Join([][]byte{prefix, expired, older, other, newest}, nil)

	got, err := extractKrb5Ticket(cache, "acct.file.core.windows.net", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := append(append([]byte{}, prefix...), newest...); !bytes.Equal(got, want) {
		t.Fatalf("extracted ccache mismatch")
	}
	if bytes.Contains(got, []byte("other")) || bytes.Contains(got, []byte("older")) {
		t.Fatalf("extracted ccache contains unrelated tickets")
	}

	if _, err := extractKrb5Ticket(cache, "missing.file.core.windows.net", now); !errors.Is(err, errKrb5TicketNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := extractKrb5Ticket(bytes.Join([][]byte{prefix, expired}, nil), "acct.file.core.windows.net", now); !errors.Is(err, errKrb5TicketNotFound) {
		t.Fatalf("expected expired ticket to be ignored, got %v", err)
	}
	if _, err := extractKrb5Ticket(cache[:len(cache)-3], "acct.file.core.windows.net", now); err == nil {
		t.Fatalf("expected truncated ccache error")
	}
	bad := append([]byte{}, cache...)
	binary.BigEndian.PutUint16(bad, 0x0503)
	if _, err := extractKrb5Ticket(bad, "acct.file.core.windows.net", now); err == nil {
		t.Fatalf("expected unsupported version error")
	}
}

func TestGetKataKrb5Ccache(t *testing.T) {
	origPath, origSet := krb5CcachePath, setCredentialCacheFunc
	defer func() { krb5CcachePath, setCredentialCacheFunc = origPath, origSet }()

	krb5CcachePath = filepath.Join(t.TempDir(), "krb5cc_0")
	end := uint32(time.Now().Add(time.Hour).Unix())
	fresh := bytes.Join([][]byte{testCcachePrefix(), testCredential("acct.file.core.windows.net", end, "fresh")}, nil)

	var refreshed int
	setCredentialCacheFunc = func(server, clientID, _, _, _, _, _ string) ([]byte, error) {
		refreshed++
		if server != "acct.file.core.windows.net" || clientID != "client-id" {
			t.Fatalf("unexpected refresh args %q %q", server, clientID)
		}
		return nil, os.WriteFile(krb5CcachePath, fresh, 0600)
	}

	if _, err := getKataKrb5Ccache("acct.file.core.windows.net", "client-id", false); err == nil {
		t.Fatalf("expected error without managed identity refresh")
	}
	got, err := getKataKrb5Ccache("acct.file.core.windows.net", "client-id", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("expected one refresh, got %d", refreshed)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(got); !bytes.Equal(decoded, fresh) {
		t.Fatalf("unexpected ccache")
	}
	if _, err := getKataKrb5Ccache("acct.file.core.windows.net", "client-id", true); err != nil || refreshed != 1 {
		t.Fatalf("expected cached ticket without refresh, err=%v refreshes=%d", err, refreshed)
	}
}
