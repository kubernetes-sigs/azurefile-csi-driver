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

package testsuites

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/azurefile-csi-driver/pkg/azurefile"
)

func TestSMBMountAuthentication(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("mount authentication checks require awk")
	}
	const source = "//account.file.core.windows.net/share"
	for _, test := range []struct {
		name     string
		mount    string
		security string
		username string
		wantPass bool
	}{
		{"default NTLMSSP", source + " /mnt/auth cifs rw,username=account 0 0", "ntlmssp", "username=account", true},
		{"explicit NTLMSSP", source + " /mnt/auth cifs rw,sec=ntlmssp,username=account 0 0", "ntlmssp", "username=account", true},
		{"signed NTLMSSP", source + " /mnt/auth cifs rw,sec=ntlmsspi,username=account 0 0", "ntlmssp", "username=account", true},
		{"workload Kerberos", source + " /mnt/auth cifs rw,sec=krb5 0 0", "krb5", "", true},
		{"managed identity Kerberos", source + " /mnt/auth cifs rw,sec=krb5,username=client-id 0 0", "krb5", "username=client-id", true},
		{"signed Kerberos", source + " /mnt/auth cifs rw,sec=krb5i 0 0", "krb5", "", true},
		{"wrong account", source + " /mnt/auth cifs rw,username=other 0 0", "ntlmssp", "username=account", false},
		{"missing Kerberos", source + " /mnt/auth cifs rw,username=account 0 0", "krb5", "", false},
		{"wrong explicit security", source + " /mnt/auth cifs rw,sec=krb5,username=account 0 0", "ntlmssp", "username=account", false},
		{"wrong mount source", "//other.file.core.windows.net/share /mnt/auth cifs rw,username=account 0 0", "ntlmssp", "username=account", false},
		{"wrong mount target", source + " /mnt/other cifs rw,username=account 0 0", "ntlmssp", "username=account", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(awk, "-v", "source="+source, "-v", "security=sec="+test.security,
				"-v", "username="+test.username, smbMountCheckScript)
			command.Stdin = strings.NewReader(test.mount + "\n")
			output, err := command.CombinedOutput()
			if err != nil {
				if _, ok := err.(*exec.ExitError); !ok {
					t.Fatalf("executing mount check: %v", err)
				}
			}
			if (err == nil) != test.wantPass {
				t.Fatalf("mount check success = %t, want %t: %v\n%s", err == nil, test.wantPass, err, output)
			}
		})
	}
}

func TestCSIInlineWorkloadIdentityDoesNotAddKeyCredentials(t *testing.T) {
	attributes := map[string]string{
		"storageAccount": "account",
		"shareName":      "share",
		"clientID":       "workload-client",
	}
	pod := NewTestPod(nil, &v1.Namespace{}, "true", false, "")
	pod.SetServiceAccountName("workload-sa")
	pod.SetupCSIInlineVolumeWithAttributes("identity", "/mnt/identity", attributes, false)
	volume := pod.pod.Spec.Volumes[0].CSI
	if volume.Driver != azurefile.DefaultDriverName || !reflect.DeepEqual(volume.VolumeAttributes, attributes) {
		t.Fatalf("inline CSI volume changed identity attributes: %v", volume)
	}
	if volume.NodePublishSecretRef != nil {
		t.Fatal("inline WI-key test must not acquire credentials from a Secret")
	}
	if pod.pod.Spec.ServiceAccountName != "workload-sa" || pod.pod.Spec.Containers[0].VolumeMounts[0].MountPath != "/mnt/identity" {
		t.Fatal("inline volume must retain the service account and mount target")
	}
}

func TestLegacyCSIInlineVolumeRetainsMountOptions(t *testing.T) {
	pod := NewTestPod(nil, &v1.Namespace{}, "true", false, "")
	pod.SetupCSIInlineVolume("legacy", "/mnt/legacy", "secret", "share", "server", true)
	volume := pod.pod.Spec.Volumes[0].CSI
	if volume.VolumeAttributes["secretName"] != "secret" || volume.VolumeAttributes["shareName"] != "share" ||
		volume.VolumeAttributes["server"] != "server" || volume.VolumeAttributes["mountOptions"] != "dir_mode=0755,file_mode=0721,cache=singleclient" ||
		volume.ReadOnly == nil || !*volume.ReadOnly {
		t.Fatalf("legacy inline volume behavior changed: %v", volume)
	}
}
