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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
)

type authenticationTokenState struct {
	hash    [32]byte
	expires time.Time
}

func AuthenticationCredentialExpiry(token string) (time.Time, error) {
	state, err := authenticationTokenMetadata(token)
	return state.expires, err
}

// Only metadata leaves this function; errors must never include the bearer token.
func authenticationTokenMetadata(token string) (authenticationTokenState, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return authenticationTokenState{}, fmt.Errorf("expected a JWT credential")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return authenticationTokenState{}, fmt.Errorf("invalid JWT credential payload encoding")
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Expires <= 0 {
		return authenticationTokenState{}, fmt.Errorf("credential has no valid expiry claim")
	}
	return authenticationTokenState{hash: sha256.Sum256([]byte(token)), expires: time.Unix(claims.Expires, 0)}, nil
}

// RunCredentialRenewal preserves the pod UID, volume and active mount through
// unchanged republish, a real credential replacement, and the original JWT's expiry.
// This does not assert destruction/expiry of an already authenticated kernel SMB session.
func (t *AuthenticationMountTest) RunCredentialRenewal(ctx context.Context, f *framework.Framework, namespace *v1.Namespace, replaceOAuthSecret func(context.Context) error) {
	pvc := t.prepare(ctx, f.ClientSet, namespace)
	marker := uuid.NewString()
	pod := t.newPod(f.ClientSet, namespace, pvc, fmt.Sprintf("printf '%%s' '%s' > /mnt/auth/data && sync && sleep 86400", marker))
	pod.Create(ctx)
	ginkgo.DeferCleanup(pod.CleanupAndWait)
	pod.WaitForRunning(ctx)
	originalUID := pod.pod.UID
	// The API server assigns NodeName after creation, not in the original pod object.
	current, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, pod.pod.Name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	node, err := authenticationNodePod(ctx, f.ClientSet, current.Spec.NodeName)
	framework.ExpectNoError(err)
	originalMountID := ""
	check := func() {
		current, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, pod.pod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err)
		gomega.Expect(current.UID).To(gomega.Equal(originalUID), "renewal must not recreate the application pod")
		authenticationExpectIO(ctx, f, current,
			fmt.Sprintf("%s && test \"$(cat /mnt/auth/data)\" = '%s' && date > /mnt/auth/renewal && sync && test -s /mnt/auth/renewal", t.mountCheck(), marker))
		mountID, err := authenticationExec(ctx, f, current, "volume-tester", `awk '$5 == "/mnt/auth" {print $1}' /proc/self/mountinfo`)
		framework.ExpectNoError(err)
		gomega.Expect(mountID).NotTo(gomega.BeEmpty(), "the application's original mount must remain present")
		if originalMountID == "" {
			originalMountID = mountID
		} else {
			gomega.Expect(mountID).To(gomega.Equal(originalMountID), "credential renewal must preserve the original active mount")
		}
	}
	check()
	oauth := t.VolumeAttributes["mountWithOAuthToken"] == "true"
	path := ""
	if !oauth {
		path, err = authenticationExec(ctx, f, node, "azurefile",
			fmt.Sprintf("find /var/lib/kubelet/plugins/file.csi.azure.com -maxdepth 1 -type f \\( -name '%s-%s' -o -name '%s-%s-*' \\)",
				t.VolumeAttributes["clientID"], t.AccountName, t.VolumeAttributes["clientID"], t.AccountName))
		framework.ExpectNoError(err)
		gomega.Expect(strings.HasPrefix(path, "/var/lib/kubelet/plugins/file.csi.azure.com/") && !strings.Contains(path, "\n")).To(gomega.BeTrue(), "exactly this fixture's dedicated token file must exist")
	}
	readState := func(ctx context.Context) (authenticationTokenState, error) {
		if oauth {
			secret, err := f.ClientSet.CoreV1().Secrets(namespace.Name).Get(ctx, t.VolumeAttributes["secretName"], metav1.GetOptions{})
			if err != nil {
				return authenticationTokenState{}, err
			}
			return authenticationTokenMetadata(string(secret.Data["oauthtoken"]))
		}
		token, err := authenticationExec(ctx, f, node, "azurefile", "cat '"+path+"'")
		if err != nil {
			return authenticationTokenState{}, fmt.Errorf("cannot read this fixture's token cache file")
		}
		return authenticationTokenMetadata(token)
	}
	original, err := readState(ctx)
	framework.ExpectNoError(err)
	gomega.Expect(time.Until(original.expires) > 0).To(gomega.BeTrue(), "original credential must still be valid")
	if !oauth {
		gomega.Expect(time.Until(original.expires) < 2*time.Hour).To(gomega.BeTrue(), "original credential must expire within this slow lane's bound")
	}
	since := time.Now()
	server := fmt.Sprintf("%s.file.%s", t.AccountName, t.StorageEndpointSuffix)
	if oauth {
		AuthenticationLogObserved(ctx, f.ClientSet, "node", current.Spec.NodeName, since,
			"OAuth token unchanged for server "+server+", skipping refresh")
	} else {
		AuthenticationLogObserved(ctx, f.ClientSet, "node", current.Spec.NodeName, since,
			"the token file("+path+") already exists and the token value is the same")
		if t.EntryPoint == "inline" {
			authenticationObserveWIRefresh(ctx, f.ClientSet, current.Spec.NodeName, since, path, string(originalUID))
		}
	}
	unchanged, err := readState(ctx)
	framework.ExpectNoError(err)
	gomega.Expect(unchanged.hash == original.hash).To(gomega.BeTrue(), "unchanged republish must precede actual rotation")
	since = time.Now()
	if oauth {
		gomega.Expect(replaceOAuthSecret).NotTo(gomega.BeNil())
		framework.ExpectNoError(replaceOAuthSecret(ctx))
	}
	gomega.Eventually(ctx, func(ctx context.Context) bool {
		state, err := readState(ctx)
		return err == nil && state.hash != original.hash && state.expires.After(original.expires) && state.expires.After(time.Now())
	}, 20*time.Minute, 10*time.Second).Should(gomega.BeTrue(), "a different, valid, later-expiring credential must reach the existing volume")
	if oauth {
		AuthenticationLogObserved(ctx, f.ClientSet, "node", current.Spec.NodeName, since,
			"refreshed credential cache for server "+server, "using secret "+namespace.Name+"/"+t.VolumeAttributes["secretName"])
	} else {
		if t.EntryPoint == "inline" {
			// Log sinceTime has second precision; exclude earlier unchanged-token refreshes.
			since = time.Now().Truncate(time.Second).Add(time.Second)
		}
		authenticationObserveWIRefresh(ctx, f.ClientSet, current.Spec.NodeName, since, path, string(originalUID))
	}
	check()
	if oauth {
		// Managed-identity storage tokens live about 24 hours, so outliving the
		// original OAuth token is outside this lane's budget.
		t.expectAccountKeySecrets(ctx, f.ClientSet, namespace)
		return
	}
	ginkgo.By("keeping the original mount through the original JWT's actual expiry")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	expiry := time.NewTimer(time.Until(original.expires.Add(time.Minute)))
	defer expiry.Stop()
waiting:
	for {
		select {
		case <-ctx.Done():
			framework.Failf("waiting through original credential expiry: %v", ctx.Err())
		case <-ticker.C:
			check()
		case <-expiry.C:
			break waiting
		}
	}
	state, err := readState(ctx)
	framework.ExpectNoError(err)
	gomega.Expect(state.hash != original.hash && state.expires.After(time.Now())).To(gomega.BeTrue(), "renewed credential must remain valid after original JWT expiry")
	check()
	t.expectAccountKeySecrets(ctx, f.ClientSet, namespace)
}
