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
	"os"
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
	azureconfig "sigs.k8s.io/cloud-provider-azure/pkg/provider/config"
)

const (
	tagKeyValueDelimiter = "="
)

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
	if perm != mode {
		klog.V(2).Infof("chmod targetPath(%s, mode:0%o) with permissions(0%o)", targetPath, info.Mode(), mode)
		if err := os.Chmod(targetPath, mode); err != nil {
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
	return volume.SetVolumeOwnership(&VolumeMounter{path: path}, path, &gidInt64, &fsGroupChangePolicy, nil)
}

// setKeyValueInMap set key/value pair in map
// key in the map is case insensitive, if key already exists, overwrite existing value
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

const confidentialRuntimeClassHandler = "kata-cc"
const kataVMIsolationRuntimeClassHandler = "kata"

// check if runtimeClass is confidential
func isConfidentialRuntimeClass(ctx context.Context, kubeClient clientset.Interface, runtimeClassName string) (bool, error) {
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
	return runtimeClass.Handler == confidentialRuntimeClassHandler ||
		runtimeClass.Handler == kataVMIsolationRuntimeClassHandler, nil
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
