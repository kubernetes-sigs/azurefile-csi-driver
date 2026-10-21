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
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestAuthenticationRPCObservesOnlyTheRequestedOperationAndVolume(t *testing.T) {
	logs := `I0000 GRPC request: {"name":"pvc-other","secrets":"***stripped***","capacity_range":{"required_bytes":1}}
I0000 GRPC request: {"volume_id":"owned-volume","secrets":"***stripped***"}
I0000 GRPC request: {"volume_id":"owned-volume","secrets":"***stripped***","staging_target_path":"/stage"}
I0000 GRPC request: {"volume_id":"owned-volume","capacity_range":{"required_bytes":1}}
I0000 GRPC request: this is not JSON`
	if !authenticationRPCMatches(logs, map[string]string{"name": "pvc-other"}, true, "capacity_range") {
		t.Error("valid supplied-secret provisioner request was not observed")
	}
	if !authenticationRPCMatches(logs, map[string]string{"volume_id": "owned-volume"}, true, "staging_target_path") {
		t.Error("valid supplied-secret node-stage request was not observed")
	}
	if !authenticationRPCMatches(logs, map[string]string{"volume_id": "owned-volume"}, false, "capacity_range") {
		t.Error("valid keyless controller-expand request was not observed")
	}
	if authenticationRPCMatches(logs, map[string]string{"volume_id": "owned-volume"}, true, "capacity_range") {
		t.Error("node-stage/delete delivery must not count as controller-expand delivery")
	}
	if authenticationRPCMatches(logs, map[string]string{"name": "pvc-owned"}, true, "capacity_range") {
		t.Error("another PVC's provisioning request must not count")
	}
}

func TestAuthenticationWIRefreshRequiresCompletedHelperForOwnedPod(t *testing.T) {
	const (
		command = `Executing command: "azfilesauthmanager set https://account.file.core.windows.net --token-file /owned-token"`
		success = `NodeStageVolume: volume owned-volume is already mounted on /var/lib/kubelet/pods/owned-pod/volumes/mount`
		failure = `GRPC error: rpc error: code = Internal desc = setCredentialCache failed for account.file.core.windows.net with error: exit status 1`
	)
	for _, test := range []struct {
		name string
		logs string
		want bool
	}{
		{"completed", command + "\n" + success, true},
		{"attempt only", command, false},
		{"wrong order", success + "\n" + command, false},
		{"different token file", strings.Replace(command, "/owned-token", "/other-token", 1) + "\n" + success, false},
		{"different pod", command + "\n" + strings.Replace(success, "owned-pod", "other-pod", 1), false},
		{"failed then unchanged staging", command + "\n" + failure + "\n" + success, false},
		{"failed then successful retry", command + "\n" + failure + "\n" + command + "\n" + success, true},
		{"concurrent fixture failure", command + "\n" + strings.Replace(failure, "account.", "other.", 1) + "\n" + success, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := authenticationWIRefreshCompleted(test.logs, "/owned-token", "owned-pod"); got != test.want {
				t.Errorf("completed refresh = %t, want %t", got, test.want)
			}
		})
	}
}

func TestAuthenticationWIRefreshExcludesPreRotationSuccess(t *testing.T) {
	beforeRotation := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	rotationObserved := beforeRotation.Add(500 * time.Millisecond)
	afterRotation := rotationObserved.Truncate(time.Second).Add(time.Second)
	command := `Executing command: "azfilesauthmanager set https://account.file.core.windows.net --token-file /owned-token"`
	events := []struct {
		at   time.Time
		line string
	}{
		{beforeRotation, command},
		{beforeRotation, "NodeStageVolume: volume owned-volume is already mounted on /var/lib/kubelet/pods/owned-pod/volumes/mount"},
		{afterRotation, command},
		{afterRotation, "GRPC error: setCredentialCache failed for account.file.core.windows.net"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/log") {
			since, err := time.Parse(time.RFC3339, r.URL.Query().Get("sinceTime"))
			if err != nil {
				t.Errorf("log request must include its observation boundary: %v", err)
				http.Error(w, "invalid sinceTime", http.StatusBadRequest)
				return
			}
			for _, event := range events {
				if !event.at.Before(since) {
					_, _ = w.Write([]byte(event.line + "\n"))
				}
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(&v1.PodList{
			Items: []v1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "node-plugin", Namespace: "kube-system"}}},
		}); err != nil {
			t.Errorf("serving node plugin: %v", err)
		}
	}))
	defer server.Close()
	client, err := clientset.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		since time.Time
		want  bool
	}{
		{"old window contains unchanged-token success", beforeRotation, true},
		{"subsecond boundary is rounded down by the API", rotationObserved, true},
		{"rotation window excludes unchanged-token success", afterRotation, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := authenticationLogsMatch(context.Background(), client, "node", "worker", test.since, func(logs string) bool {
				return authenticationWIRefreshCompleted(logs, "/owned-token", "owned-pod")
			})
			if err != nil || got != test.want {
				t.Fatalf("completed refresh = %t, want %t, error: %v", got, test.want, err)
			}
		})
	}
}

func TestAuthenticationKeySecretGuardIgnoresUnrelatedAccounts(t *testing.T) {
	unrelated := v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated"}, Data: map[string][]byte{
		"azurestorageaccountname": []byte("other"), "azurestorageaccountkey": []byte("test-key"),
	}}
	for _, test := range []struct {
		name       string
		secretName string
		secret     v1.Secret
		wantError  bool
	}{
		{"unrelated key is allowed", "", unrelated, false},
		{"fixture account key is rejected", "", v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "fixture"}, Data: map[string][]byte{
			"azurestorageaccountname": []byte("account"), "azurestorageaccountkey": []byte("test-key"),
		}}, true},
		{"generated lookup name is rejected even with wrong account", "", v1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "azure-storage-account-account-secret"}, Data: unrelated.Data,
		}, true},
		{"explicit lookup name is rejected even with wrong account", "unrelated", unrelated, true},
		{"legacy case-insensitive fields are rejected", "", v1.Secret{Data: map[string][]byte{
			"AccountName": []byte("ACCOUNT"), "AccountKey": []byte("test-key"),
		}}, true},
		{"token-only Secret is allowed", "oauth", v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "oauth"}, Data: map[string][]byte{
			"azurestorageaccountname": []byte("account"), "oauthtoken": []byte("test-token"),
		}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateNoAuthenticationAccountKeySecrets([]v1.Secret{unrelated, test.secret}, "account", test.secretName)
			if (err != nil) != test.wantError {
				t.Fatalf("credential guard error = %v, want error = %t", err, test.wantError)
			}
		})
	}
}

func TestAuthenticationOperationSecretsRemainStrictlyScoped(t *testing.T) {
	makeSecret := func(name, account, key string) v1.Secret {
		return v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string][]byte{
			"azurestorageaccountname": []byte(account), "azurestorageaccountkey": []byte(key),
		}}
	}
	refs := AuthenticationRequestSecrets{Provisioner: "provisioner", NodeStage: "stage", ControllerExpand: "expand"}
	valid := []v1.Secret{
		makeSecret("provisioner", "account", "test-key"),
		makeSecret("stage", "account", "test-key"),
		makeSecret("expand", "account", "test-key"),
	}
	if err := validateAuthenticationAccountKeySecrets(valid, refs, "account"); err != nil {
		t.Fatalf("valid operation credentials rejected: %v", err)
	}
	for _, test := range []struct {
		name    string
		secrets []v1.Secret
		refs    AuthenticationRequestSecrets
	}{
		{"default still forbids keys", valid, AuthenticationRequestSecrets{}},
		{"unreferenced generated key", append(append([]v1.Secret{}, valid...), makeSecret("generated", "account", "key")), refs},
		{"missing expansion Secret", valid[:2], refs},
		{"wrong account", []v1.Secret{makeSecret("provisioner", "other", "key")}, AuthenticationRequestSecrets{Provisioner: "provisioner"}},
		{"empty key", []v1.Secret{makeSecret("provisioner", "account", "")}, AuthenticationRequestSecrets{Provisioner: "provisioner"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAuthenticationAccountKeySecrets(test.secrets, test.refs, "account"); err == nil {
				t.Error("masking or invalid account credential accepted")
			}
		})
	}
	if err := validateAuthenticationAccountKeySecrets(nil, AuthenticationRequestSecrets{}, "account"); err != nil {
		t.Errorf("default keyless namespace rejected: %v", err)
	}
}

func TestAuthenticationCredentialMetadataUsesRealExpiryAndHash(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1900000000}`))
	token := "header." + payload + ".signature"
	state, err := authenticationTokenMetadata(token)
	if err != nil {
		t.Fatal(err)
	}
	if !state.expires.Equal(time.Unix(1900000000, 0)) {
		t.Error("JWT expiry was not preserved")
	}
	replacement, err := authenticationTokenMetadata(token + "-changed")
	if err != nil || replacement.hash == state.hash {
		t.Error("a changed credential must have a changed hash")
	}
	for _, invalid := range []string{"bearer-secret", "header.!!!.signature", "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":0}`)) + ".signature"} {
		_, err := authenticationTokenMetadata(invalid)
		if err == nil {
			t.Error("invalid credential metadata accepted")
		} else if strings.Contains(err.Error(), invalid) {
			t.Error("credential must not appear in validation errors")
		}
	}
}
