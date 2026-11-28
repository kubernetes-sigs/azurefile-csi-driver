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
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

const (
	kataSensitiveMountOptionsKey = "sensitiveMountOptions"
	kataKrb5CcacheKey            = "krb5Ccache"
	// Tickets about to expire are refreshed so the guest mount does not fail mid-handoff.
	krb5TicketMinValidity = 5 * time.Minute
)

// krb5CcachePath is the node Kerberos cache that azfilesauthmanager writes for uid 0.
var krb5CcachePath = "/var/lib/kubelet/kerberos/krb5cc_0"

var setCredentialCacheFunc = setCredentialCache

const krb5CcacheVersion4 = 0x0504

var errKrb5TicketNotFound = errors.New("kerberos ticket not found")

type krb5CcacheReader struct {
	data []byte
	off  int
}

func (r *krb5CcacheReader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.off {
		return nil, fmt.Errorf("truncated kerberos ccache at offset %d", r.off)
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *krb5CcacheReader) u16() (uint16, error) {
	b, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (r *krb5CcacheReader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *krb5CcacheReader) octets() ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	return r.take(int(n))
}

// principal returns the principal components (realm excluded).
func (r *krb5CcacheReader) principal() ([]string, error) {
	if _, err := r.u32(); err != nil { // name type
		return nil, err
	}
	count, err := r.u32()
	if err != nil {
		return nil, err
	}
	if _, err := r.octets(); err != nil { // realm
		return nil, err
	}
	if int(count) > len(r.data) {
		return nil, fmt.Errorf("invalid kerberos principal component count %d", count)
	}
	components := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		c, err := r.octets()
		if err != nil {
			return nil, err
		}
		components = append(components, string(c))
	}
	return components, nil
}

// skipTagged skips a counted list of (uint16 type, counted octets) entries.
func (r *krb5CcacheReader) skipTagged() error {
	count, err := r.u32()
	if err != nil {
		return err
	}
	for i := uint32(0); i < count; i++ {
		if _, err := r.u16(); err != nil {
			return err
		}
		if _, err := r.octets(); err != nil {
			return err
		}
	}
	return nil
}

// extractKrb5Ticket returns a new ccache containing only the newest unexpired
// cifs/<server> credential from ccache, so tickets for other storage accounts on
// the node are never handed to a guest.
func extractKrb5Ticket(ccache []byte, server string, now time.Time) ([]byte, error) {
	r := &krb5CcacheReader{data: ccache}
	version, err := r.u16()
	if err != nil {
		return nil, err
	}
	if version != krb5CcacheVersion4 {
		return nil, fmt.Errorf("unsupported kerberos ccache version 0x%04x", version)
	}
	if _, err := r.octets16(); err != nil { // header tags
		return nil, err
	}
	if _, err := r.principal(); err != nil { // default principal
		return nil, err
	}
	prefix := ccache[:r.off]

	var best []byte
	var bestEnd uint32
	for r.off < len(ccache) {
		start := r.off
		if _, err := r.principal(); err != nil { // client
			return nil, err
		}
		serverPrincipal, err := r.principal()
		if err != nil {
			return nil, err
		}
		if _, err := r.u16(); err != nil { // key enctype
			return nil, err
		}
		if _, err := r.octets(); err != nil { // key
			return nil, err
		}
		if _, err := r.take(8); err != nil { // authtime, starttime
			return nil, err
		}
		endTime, err := r.u32()
		if err != nil {
			return nil, err
		}
		if _, err := r.take(4 + 1 + 4); err != nil { // renew_till, is_skey, flags
			return nil, err
		}
		if err := r.skipTagged(); err != nil { // addresses
			return nil, err
		}
		if err := r.skipTagged(); err != nil { // authdata
			return nil, err
		}
		if _, err := r.octets(); err != nil { // ticket
			return nil, err
		}
		if _, err := r.octets(); err != nil { // second ticket
			return nil, err
		}

		if len(serverPrincipal) == 2 && serverPrincipal[0] == "cifs" &&
			strings.EqualFold(serverPrincipal[1], server) &&
			int64(endTime) > now.Unix() && endTime > bestEnd {
			best = ccache[start:r.off]
			bestEnd = endTime
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w for cifs/%s", errKrb5TicketNotFound, server)
	}

	out := make([]byte, 0, len(prefix)+len(best))
	out = append(out, prefix...)
	return append(out, best...), nil
}

func (r *krb5CcacheReader) octets16() ([]byte, error) {
	n, err := r.u16()
	if err != nil {
		return nil, err
	}
	return r.take(int(n))
}

// getKataKrb5Ccache returns a base64 ccache holding only the ticket for krbHost.
// With managed identity, a missing or expiring ticket is refreshed via IMDS.
func getKataKrb5Ccache(krbHost, clientID string, refreshWithMI bool) (string, error) {
	read := func() ([]byte, error) {
		data, err := os.ReadFile(krb5CcachePath)
		if err != nil {
			return nil, err
		}
		return extractKrb5Ticket(data, krbHost, time.Now().Add(krb5TicketMinValidity))
	}

	ticket, err := read()
	if err != nil && refreshWithMI && (errors.Is(err, errKrb5TicketNotFound) || errors.Is(err, fs.ErrNotExist)) {
		if clientID == "" {
			return "", fmt.Errorf("clientID is required to refresh managed identity ticket")
		}
		if out, setErr := setCredentialCacheFunc(krbHost, clientID, "", "", "", "", ""); setErr != nil {
			return "", fmt.Errorf("setCredentialCache failed: %w, output: %s", setErr, out)
		}
		ticket, err = read()
	}
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ticket), nil
}
