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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"sigs.k8s.io/azurefile-csi-driver/test/e2e/driver"
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

// authenticationTicketStart returns the newest Kerberos ticket start (epoch
// seconds) for server in the node cache, or 0. The listing has no secret material.
func authenticationTicketStart(ctx context.Context, f *framework.Framework, node *v1.Pod, server string) (int64, error) {
	out, err := authenticationExec(ctx, f, node, "azurefile", "azfilesauthmanager list --json")
	if err != nil {
		return 0, fmt.Errorf("cannot list the node Kerberos cache")
	}
	var tickets []struct {
		Server string `json:"server"`
		Start  string `json:"ticket_start_time"`
	}
	if err := json.Unmarshal([]byte(out), &tickets); err != nil {
		return 0, fmt.Errorf("unexpected Kerberos cache listing")
	}
	var newest int64
	for _, ticket := range tickets {
		if !strings.HasPrefix(ticket.Server, "cifs/"+server+"@") {
			continue
		}
		var started int64
		if _, epoch, found := strings.Cut(ticket.Start, "(epoch: "); found {
			started, _ = strconv.ParseInt(strings.TrimSuffix(epoch, ")"), 10, 64)
		}
		newest = max(newest, started)
	}
	return newest, nil
}

// PreProvisionedCredentialRenewalTest will provision a required PV and PVC, or a CSI inline volume, and a long-running Pod
// Testing if the Pod keeps its original mount and data access while the mount credential is republished,
// rotated and, for workload identity tokens, outlives the original token's expiry.
// It does not assert that an already authenticated kernel SMB session is torn down.
type PreProvisionedCredentialRenewalTest struct {
	CSIDriver       driver.PreProvisionedVolumeTestDriver
	Pod             PodDetails
	CSIInlineVolume bool
	// Server is the SMB host, <account>.file.<storage endpoint suffix>
	Server string
	// TokenCacheFile is the node plugin's workload identity token file name prefix, <clientID>-<account>
	TokenCacheFile string
	// OAuthSecretName is the mountWithOAuthToken Secret; RotateOAuthSecret replaces its token
	OAuthSecretName   string
	RotateOAuthSecret func(ctx context.Context) error
}

func (t *PreProvisionedCredentialRenewalTest) Run(ctx context.Context, f *framework.Framework, namespace *v1.Namespace) {
	mountPath := t.Pod.Volumes[0].VolumeMount.MountPathGenerate + "1"
	marker := uuid.NewString()
	longRunning := t.Pod
	longRunning.Cmd = fmt.Sprintf("%s && echo '%s' > %s/data && sleep 86400", t.Pod.Cmd, marker, mountPath)
	var tpod *TestPod
	if t.CSIInlineVolume {
		tpod, _ = longRunning.SetupWithCSIInlineVolumes(f.ClientSet, namespace, "", "", "", false)
	} else {
		var cleanup []func(ctx context.Context)
		tpod, cleanup = longRunning.SetupWithPreProvisionedVolumes(ctx, f.ClientSet, namespace, t.CSIDriver)
		// defer must be called here for resources not get removed before using them
		for i := range cleanup {
			defer cleanup[i](ctx)
		}
	}
	ginkgo.By("deploying the pod")
	tpod.Create(ctx)
	defer tpod.Cleanup(ctx)
	tpod.WaitForRunning(ctx)
	// The API server assigns NodeName after creation, not in the original pod object.
	created, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, tpod.pod.Name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	node, err := authenticationNodePod(ctx, f.ClientSet, created.Spec.NodeName)
	framework.ExpectNoError(err)

	originalMountID := ""
	check := func() {
		current, err := f.ClientSet.CoreV1().Pods(namespace.Name).Get(ctx, tpod.pod.Name, metav1.GetOptions{})
		framework.ExpectNoError(err)
		gomega.Expect(current.UID).To(gomega.Equal(created.UID), "renewal must not recreate the application pod")
		authenticationExpectIO(ctx, f, current, fmt.Sprintf("%s && grep -q '%s' %s/data", t.Pod.Cmd, marker, mountPath))
		mountID, err := authenticationExec(ctx, f, current, "volume-tester", fmt.Sprintf(`awk '$5 == "%s" {print $1}' /proc/self/mountinfo`, mountPath))
		framework.ExpectNoError(err)
		gomega.Expect(mountID).NotTo(gomega.BeEmpty(), "the application's original mount must remain present")
		if originalMountID == "" {
			originalMountID = mountID
		}
		gomega.Expect(mountID).To(gomega.Equal(originalMountID), "credential renewal must preserve the original active mount")
	}
	check()

	oauth := t.OAuthSecretName != ""
	path := ""
	if !oauth {
		path, err = authenticationExec(ctx, f, node, "azurefile",
			fmt.Sprintf("find /var/lib/kubelet/plugins/file.csi.azure.com -maxdepth 1 -type f \\( -name '%s' -o -name '%s-*' \\)", t.TokenCacheFile, t.TokenCacheFile))
		framework.ExpectNoError(err)
		gomega.Expect(strings.HasPrefix(path, "/var/lib/kubelet/plugins/file.csi.azure.com/") && !strings.Contains(path, "\n")).To(gomega.BeTrue(), "exactly this volume's token file must exist")
	}
	readState := func(ctx context.Context) (authenticationTokenState, error) {
		if oauth {
			secret, err := f.ClientSet.CoreV1().Secrets(namespace.Name).Get(ctx, t.OAuthSecretName, metav1.GetOptions{})
			if err != nil {
				return authenticationTokenState{}, err
			}
			return authenticationTokenMetadata(string(secret.Data["oauthtoken"]))
		}
		token, err := authenticationExec(ctx, f, node, "azurefile", "cat '"+path+"'")
		if err != nil {
			return authenticationTokenState{}, fmt.Errorf("cannot read this volume's token cache file")
		}
		return authenticationTokenMetadata(token)
	}
	original, err := readState(ctx)
	framework.ExpectNoError(err)
	gomega.Expect(time.Until(original.expires) > 0).To(gomega.BeTrue(), "original credential must still be valid")
	if !oauth {
		gomega.Expect(time.Until(original.expires) < 2*time.Hour).To(gomega.BeTrue(), "original credential must expire within this slow lane's bound")
	}
	ticket := func(ctx context.Context) int64 {
		started, err := authenticationTicketStart(ctx, f, node, t.Server)
		framework.ExpectNoError(err)
		return started
	}
	// The node Kerberos cache is the credential the kernel uses; a later ticket
	// for this volume's server proves the driver re-authenticated.
	refreshedAfter := func(baseline int64, reason string) {
		gomega.Eventually(ctx, ticket, 5*time.Minute, 10*time.Second).Should(gomega.BeNumerically(">", baseline), reason)
	}
	baseline := ticket(ctx)
	gomega.Expect(baseline).To(gomega.BeNumerically(">", 0), "the mount must have a Kerberos ticket for this volume's server")
	inline := !oauth && t.CSIInlineVolume
	if inline {
		ginkgo.By("checking that inline republish refreshes the Kerberos ticket with an unchanged token")
		refreshedAfter(baseline, "inline republish must refresh the credential cache with an unchanged token")
	}
	unchanged, err := readState(ctx)
	framework.ExpectNoError(err)
	gomega.Expect(unchanged.hash == original.hash).To(gomega.BeTrue(), "unchanged republish must precede actual rotation")
	if !inline {
		// Static WI and OAuth refresh only when the token changes.
		baseline = ticket(ctx)
	}
	if oauth {
		ginkgo.By("rotating the OAuth token Secret")
		framework.ExpectNoError(t.RotateOAuthSecret(ctx))
	}
	ginkgo.By("waiting for a rotated credential to reach the existing volume")
	gomega.Eventually(ctx, func(ctx context.Context) bool {
		state, err := readState(ctx)
		return err == nil && state.hash != original.hash && state.expires.After(original.expires) && state.expires.After(time.Now())
	}, 20*time.Minute, 10*time.Second).Should(gomega.BeTrue(), "a different, valid, later-expiring credential must reach the existing volume")
	if inline {
		// Inline republish refreshes on every call; only one after rotation was observed uses the new token.
		baseline = ticket(ctx)
	}
	refreshedAfter(baseline, "the rotated credential must refresh the node Kerberos cache")
	check()
	if oauth {
		// Managed-identity storage tokens live about 24 hours, so outliving the
		// original OAuth token is outside this lane's budget.
		return
	}

	ginkgo.By("keeping the original mount through the original token's actual expiry")
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
	gomega.Expect(state.hash != original.hash && state.expires.After(time.Now())).To(gomega.BeTrue(), "renewed credential must remain valid after original token expiry")
	check()
}
