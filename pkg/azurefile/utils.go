/*
Copyright 2020 The Kubernetes Authors.

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
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
	mount "k8s.io/mount-utils"
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
)

const (
	tagKeyValueDelimiter = "="
)

var subscriptionIDRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// lockMap used to lock on entries
type lockMap struct {
	sync.Mutex
	mutexMap map[string]*sync.Mutex
}

// NewLockMap returns a new lock map
func newLockMap() *lockMap {
	return &lockMap{
		mutexMap: make(map[string]*sync.Mutex),
	}
}

// LockEntry acquires a lock associated with the specific entry
func (lm *lockMap) LockEntry(entry string) {
	lm.Lock()
	// check if entry does not exists, then add entry
	if _, exists := lm.mutexMap[entry]; !exists {
		lm.addEntry(entry)
	}

	lm.Unlock()
	lm.lockEntry(entry)
}

// UnlockEntry release the lock associated with the specific entry
func (lm *lockMap) UnlockEntry(entry string) {
	lm.Lock()
	defer lm.Unlock()

	if _, exists := lm.mutexMap[entry]; !exists {
		return
	}
	lm.unlockEntry(entry)
}

func (lm *lockMap) addEntry(entry string) {
	lm.mutexMap[entry] = &sync.Mutex{}
}

func (lm *lockMap) lockEntry(entry string) {
	lm.mutexMap[entry].Lock()
}

func (lm *lockMap) unlockEntry(entry string) {
	lm.mutexMap[entry].Unlock()
}

func isDiskFsType(fsType string) bool {
	for _, v := range supportedDiskFsTypeList {
		if fsType == v {
			return true
		}
	}
	return false
}

// File share names can contain only lowercase letters, numbers, and hyphens,
// and must begin and end with a letter or a number
func isSupportedShareNamePrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	if len(prefix) > 20 {
		return false
	}
	if prefix[0] == '-' {
		return false
	}
	for _, v := range prefix {
		if v != '-' && (v < '0' || v > '9') && (v < 'a' || v > 'z') {
			return false
		}
	}
	return true
}

func isSupportedFsType(fsType string) bool {
	if fsType == "" {
		return true
	}
	for _, v := range supportedFsTypeList {
		if fsType == v {
			return true
		}
	}
	return false
}

func isRetriableError(err error) bool {
	if err != nil {
		for _, v := range retriableErrors {
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(v)) {
				return true
			}
		}
	}
	return false
}

// Mount error reason classifications recorded on the mount_error_reason metric label.
const (
	mountErrorTimeout      = "timeout"
	mountErrorENOENT       = "enoent"
	mountErrorAccessDenied = "access_denied"
	mountErrorNetwork      = "network"
	mountErrorStale        = "stale"
	mountErrorBusy         = "busy"
	mountErrorOther        = "other"
)

// classifyMountError buckets a mount command failure into a small, bounded set
// of reasons so queries can separate failure modes. For dashboards the reasons
// can be grouped like:
//   - retryable/transient: timeout, network, stale
//   - ambiguous:           enoent (see note), busy (target/resource busy, often already mounted)
//   - terminal:            access_denied
//
// Note on enoent: for SMB, kernel surfaces "mount error(2): No such file or
// directory" for both an absent share (SMB2 maps STATUS_BAD_NETWORK_NAME to
// -ENOENT, terminal; see fs/smb/client/smb2maperror.c) and a transient
// tree-connect rejection when the share/account is overloaded (retryable).
// mount.cifs prints the mapped errno, not the status name
// (STATUS_BAD_NETWORK_NAME only reaches dmesg), so indistinguishable from mount
// stderr alone.
//
// The string switch runs first (mount.cifs / mount.aznfs stderr and CIFS kernel
// error codes); mount.IsCorruptedMnt is only a typed fallback so a wrapped
// syscall error with text matching a bucket (e.g. EACCES -> "permission
// denied", EHOSTDOWN -> "host is down") is classified rather than collapsed to
// stale. Returns "" when err is nil.
func classifyMountError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded"):
		// mount.cifs/mount.nfs print "timed out", non-proxy path surfaces
		// "mount operation timed out after N seconds", azurefile-proxy gRPC
		// path surfaces "context deadline exceeded".
		return mountErrorTimeout
	case strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "error(13)") ||
		strings.Contains(msg, "access denied"):
		// mount.cifs prints "mount error(13): Permission denied". mount.nfs
		// prints "access denied by server while mounting ...".
		// STATUS_LOGON_FAILURE / STATUS_ACCESS_DENIED (-EACCES) only appears in
		// dmesg, not mount stderr.
		return mountErrorAccessDenied
	case strings.Contains(msg, "error(16)") ||
		strings.Contains(msg, "device or resource busy") ||
		strings.Contains(msg, "target is busy"):
		return mountErrorBusy
	case strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "error(2)"):
		return mountErrorENOENT
	case strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "host is down") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "could not connect to") ||
		strings.Contains(msg, "could not resolve address") ||
		strings.Contains(msg, "unable to find suitable address") ||
		strings.Contains(msg, "error(101)") ||
		strings.Contains(msg, "error(111)") ||
		strings.Contains(msg, "error(112)") ||
		strings.Contains(msg, "error(113)") ||
		strings.Contains(msg, "error(115)"):
		// mount.cifs prints "mount error(<111|113|115>): could not connect to
		// <ip>" (ECONNREFUSED/EHOSTUNREACH/EINPROGRESS) while cycling addresses,
		// then "Unable to find suitable address." once exhausted; EHOSTDOWN(112)
		// prints "mount error(112): Host is down"; ENETUNREACH(101) prints
		// "mount error(101): Network is unreachable". A getaddrinfo/DNS failure
		// prints "could not resolve address for <host>" (NXDOMAIN for a
		// deleted/mistyped account, transient SERVFAIL, or DNS server
		// unreachable). mount.nfs surfaces RPC-layer "Connection refused".
		// error(110)/"connection timed out" (ETIMEDOUT) is caught by the
		// timeout case above.
		return mountErrorNetwork
	default:
		// Typed fallback for stale/broken mounts (wrapped syscall.ESTALE /
		// ENOTCONN / EIO from IsLikelyNotMountPoint on a dead mount).
		if mount.IsCorruptedMnt(err) {
			return mountErrorStale
		}
		return mountErrorOther
	}
}

func isThrottlingError(err error) bool {
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		return strings.Contains(errMsg, strings.ToLower(tooManyRequests)) || strings.Contains(errMsg, clientThrottled)
	}
	return false
}

func sleepIfThrottled(err error, defaultSleepSec int) {
	if isThrottlingError(err) {
		retryAfter := getRetryAfterSeconds(err)
		if retryAfter == 0 {
			retryAfter = defaultSleepSec
		}
		klog.Warningf("sleep %d more seconds, waiting for throttling complete", retryAfter)
		time.Sleep(time.Duration(retryAfter) * time.Second)
	}
}

// getRetryAfterSeconds returns the number of seconds to wait from the error message
func getRetryAfterSeconds(err error) int {
	if err == nil {
		return 0
	}
	re := regexp.MustCompile(`RetryAfter: (\d+)s`)
	match := re.FindStringSubmatch(err.Error())
	if len(match) > 1 {
		if retryAfter, err := strconv.Atoi(match[1]); err == nil {
			if retryAfter > maxThrottlingSleepSec {
				return maxThrottlingSleepSec
			}
			return retryAfter
		}
	}
	return 0
}

func createStorageAccountSecret(account, key string) map[string]string {
	secret := make(map[string]string)
	secret[defaultSecretAccountName] = account
	secret[defaultSecretAccountKey] = key
	return secret
}

func ConvertTagsToMap(tags string, tagsDelimiter string) (map[string]string, error) {
	m := make(map[string]string)
	if tags == "" {
		return m, nil
	}
	if tagsDelimiter == "" {
		tagsDelimiter = ","
	}
	s := strings.Split(tags, tagsDelimiter)
	for _, tag := range s {
		kv := strings.SplitN(tag, tagKeyValueDelimiter, 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("Tags '%s' are invalid, the format should like: 'key1=value1%skey2=value2'", tags, tagsDelimiter)
		}
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, fmt.Errorf("Tags '%s' are invalid, the format should like: 'key1=value1%skey2=value2'", tags, tagsDelimiter)
		}
		value := strings.TrimSpace(kv[1])
		m[key] = value
	}
	return m, nil
}

type VolumeMounter struct {
	path       string
	attributes volume.Attributes
}

func (l *VolumeMounter) GetPath() string {
	return l.path
}

func (l *VolumeMounter) GetAttributes() volume.Attributes {
	return l.attributes
}

func (l *VolumeMounter) CanMount() error {
	return nil
}

func (l *VolumeMounter) SetUp(_ volume.MounterArgs) error {
	return nil
}

func (l *VolumeMounter) SetUpAt(_ string, _ volume.MounterArgs) error {
	return nil
}

func (l *VolumeMounter) GetMetrics() (*volume.Metrics, error) {
	return nil, nil
}

// chmodIfPermissionMismatch only perform chmod when permission mismatches
func chmodIfPermissionMismatch(targetPath string, mode os.FileMode) error {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	perm := info.Mode() & os.ModePerm
	expectedPerms := mode & os.ModePerm
	if perm != expectedPerms {
		klog.V(2).Infof("chmod targetPath(%s, mode:0%o) with permissions(0%o)", targetPath, info.Mode(), expectedPerms)
		// only change the permission mode bits, keep the other bits as is
		if err := os.Chmod(targetPath, (info.Mode()&^os.ModePerm)|os.FileMode(expectedPerms)); err != nil {
			return err
		}
	} else {
		klog.V(2).Infof("skip chmod on targetPath(%s) since mode is already 0%o)", targetPath, info.Mode())
	}
	return nil
}

// SetVolumeOwnership would set gid for path recursively
func SetVolumeOwnership(path, gid, policy string) error {
	id, err := strconv.Atoi(gid)
	if err != nil {
		return fmt.Errorf("convert %s to int failed with %v", gid, err)
	}
	gidInt64 := int64(id)
	fsGroupChangePolicy := v1.FSGroupChangeOnRootMismatch
	if policy != "" {
		fsGroupChangePolicy = v1.PodFSGroupChangePolicy(policy)
	}
	return volume.NewVolumeOwnership(&VolumeMounter{path: path}, path, &gidInt64, &fsGroupChangePolicy, nil).ChangePermissions()
}

// setKeyValueInMap set key/value pair in map
// key in the map is case insensitive, if key already exists, overwrite existing value
// caseCollidingKey reports the first pair of keys in m that collide under
// Unicode case folding. The returned string contains the two lowercased keys
// in lexical order.
func caseCollidingKey(m map[string]string) (string, bool) {
	// Since context map contains very limited values, we can use a simple O(n^2) algorithm to check for UNICODE case-insensitive collisions.
	for validatingKey := range m {
		for curKey := range m {
			if validatingKey != curKey {
				if strings.EqualFold(validatingKey, curKey) {
					firstKey := strings.ToLower(validatingKey)
					secondKey := strings.ToLower(curKey)
					if secondKey < firstKey {
						firstKey, secondKey = secondKey, firstKey
					}
					return fmt.Sprintf("%s, %s", firstKey, secondKey), true
				}
			}
		}
	}
	return "", false
}

func setKeyValueInMap(m map[string]string, key, value string) {
	if m == nil {
		return
	}
	for k := range m {
		if strings.EqualFold(k, key) {
			m[k] = value
			return
		}
	}
	m[key] = value
}

// getValueInMap get value from map by key
// key in the map is case insensitive
func getValueInMap(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// replaceWithMap replace key with value for str
func replaceWithMap(str string, m map[string]string) string {
	for k, v := range m {
		if k != "" {
			str = strings.ReplaceAll(str, k, v)
		}
	}
	return str
}

func isReadOnlyFromCapability(vc *csi.VolumeCapability) bool {
	if vc.GetAccessMode() == nil {
		return false
	}
	mode := vc.GetAccessMode().GetMode()
	return (mode == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY ||
		mode == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY)
}

// check if runtimeClass is confidential
func isConfidentialRuntimeClass(ctx context.Context, kubeClient clientset.Interface, runtimeClassName, runtimeClassHandler string) (bool, error) {
	// if runtimeClassName is empty, return false
	if runtimeClassName == "" {
		return false, nil
	}
	if kubeClient == nil {
		return false, fmt.Errorf("kubeClient is nil")
	}
	runtimeClassClient := kubeClient.NodeV1().RuntimeClasses()
	runtimeClass, err := runtimeClassClient.Get(ctx, runtimeClassName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	klog.V(4).Infof("runtimeClass %s handler: %s", runtimeClassName, runtimeClass.Handler)
	return runtimeClass.Handler == runtimeClassHandler, nil
}

// getBackOff returns a backoff object based on the config
func getBackOff(config azureconfig.Config) wait.Backoff {
	steps := config.CloudProviderBackoffRetries
	if steps < 1 {
		steps = 1
	}
	return wait.Backoff{
		Steps:    steps,
		Factor:   config.CloudProviderBackoffExponent,
		Jitter:   config.CloudProviderBackoffJitter,
		Duration: time.Duration(config.CloudProviderBackoffDuration) * time.Second,
	}
}

func getFileServiceURL(accountName, storageEndpointSuffix string) string {
	if storageEndpointSuffix == "" {
		storageEndpointSuffix = defaultStorageEndPointSuffix
	}
	return fmt.Sprintf(serviceURLTemplate, accountName, storageEndpointSuffix)
}

func isValidSubscriptionID(subsID string) bool {
	return subscriptionIDRegex.MatchString(subsID)
}

// RemoveOptionIfExists removes the given option from the list of options
// return the new list and a boolean indicating whether the option was found.
func removeOptionIfExists(options []string, removeOption string) ([]string, bool) {
	for i, option := range options {
		if strings.EqualFold(option, removeOption) {
			return append(options[:i], options[i+1:]...), true
		}
	}
	return options, false
}

// standardv2:
//
//	MIN(MAX(1000 + CEILING(0.2 * ProvisionedStorageGiB), 500), 50000)
//
// premiumv2:
//
//	MIN(MAX(3000 + CEILING(1 * ProvisionedStorageGiB), 3000), 102400)
//
// https://learn.microsoft.com/en-us/azure/storage/files/understanding-billing#provisioned-v2-provisioning-detail
func getDefaultIOPS(requestGiB int, storageAccountType string) *int32 {
	var iops int32
	if strings.Contains(strings.ToLower(storageAccountType), standardv2) {
		iops = min(int32(math.Ceil(0.2*float64(requestGiB))+1000), 50000)
	} else if strings.Contains(strings.ToLower(storageAccountType), premiumv2) {
		iops = min(int32(requestGiB+3000), 102400)
	} else {
		return nil
	}
	return &iops
}

// standardv2:
//
//	MIN(MAX(60 + CEILING(0.02 * ProvisionedStorageGiB), 60), 5120)
//
// premiumv2:
//
//	MIN(MAX(100 + CEILING(0.1 * ProvisionedStorageGiB), 100), 10340)
//
// https://learn.microsoft.com/en-us/azure/storage/files/understanding-billing#provisioned-v2-provisioning-detail
func getDefaultBandwidth(requestGiB int, storageAccountType string) *int32 {
	var bandwidth int32
	if strings.Contains(strings.ToLower(storageAccountType), standardv2) {
		bandwidth = min(int32(math.Ceil(0.02*float64(requestGiB))+60), 5120)
	} else if strings.Contains(strings.ToLower(storageAccountType), premiumv2) {
		bandwidth = min(int32(math.Ceil(0.1*float64(requestGiB))+100), 10340)
	} else {
		return nil
	}
	return &bandwidth
}

func setCredentialCache(server, clientID string) ([]byte, error) {
	if server == "" || clientID == "" {
		return nil, fmt.Errorf("server and clientID must be provided")
	}

	cmd := exec.Command("azfilesauthmanager", "set", "https://"+server, "--imds-client-id", clientID)
	cmd.Env = append(os.Environ(), cmd.Env...)
	klog.V(2).Infof("Executing command: %q", cmd.String())
	return cmd.CombinedOutput()
}

// validateInlineVolumeMountSource validates that there are no path
// separators or dot-only segments.
func validateInlineVolumeMountSource(server, shareName string) error {
	if strings.ContainsAny(server, `/\`) || server == "." || server == ".." {
		return fmt.Errorf("invalid server %q for ephemeral volume: must be a hostname or address", server)
	}
	if strings.ContainsAny(shareName, `/\`) || shareName == "." || shareName == ".." {
		return fmt.Errorf("invalid shareName %q for ephemeral volume: must be a single share name", shareName)
	}
	return nil
}

// getSecretNamespace returns the namespace of the Secret referenced by the
// volume context, preferring an explicit secretNamespace attribute and
// falling back to PVC namespace, then to "default".
func getSecretNamespace(volumeContext map[string]string) string {
	if ns := getValueInMap(volumeContext, secretNamespaceField); ns != "" {
		return ns
	}
	if ns := getValueInMap(volumeContext, pvcNamespaceKey); ns != "" {
		return ns
	}
	return defaultNamespace
}
