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
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
)

const (
	tagsDelimiter        = ","
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

func sleepIfThrottled(err error, sleepSec int) {
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tooManyRequests)) || strings.Contains(strings.ToLower(err.Error()), clientThrottled) {
		klog.Warningf("sleep %d more seconds, waiting for throttling complete", sleepSec)
		time.Sleep(time.Duration(sleepSec) * time.Second)
	}
}

func useDataPlaneAPI(volContext map[string]string) bool {
	useDataPlaneAPI := false
	for k, v := range volContext {
		switch strings.ToLower(k) {
		case useDataPlaneAPIField:
			useDataPlaneAPI = strings.EqualFold(v, trueValue)
		}
	}
	return useDataPlaneAPI
}

func createStorageAccountSecret(account, key string) map[string]string {
	secret := make(map[string]string)
	secret[defaultSecretAccountName] = account
	secret[defaultSecretAccountKey] = key
	return secret
}

func ConvertTagsToMap(tags string) (map[string]string, error) {
	m := make(map[string]string)
	if tags == "" {
		return m, nil
	}
	s := strings.Split(tags, tagsDelimiter)
	for _, tag := range s {
		kv := strings.Split(tag, tagKeyValueDelimiter)
		if len(kv) != 2 {
			return nil, fmt.Errorf("Tags '%s' are invalid, the format should like: 'key1=value1,key2=value2'", tags)
		}
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, fmt.Errorf("Tags '%s' are invalid, the format should like: 'key1=value1,key2=value2'", tags)
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

func (l *VolumeMounter) SetUp(mounterArgs volume.MounterArgs) error {
	return nil
}

func (l *VolumeMounter) SetUpAt(dir string, mounterArgs volume.MounterArgs) error {
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

// replaceWithMap replace key with value for str
func replaceWithMap(str string, m map[string]string) string {
	for k, v := range m {
		if k != "" {
			str = strings.ReplaceAll(str, k, v)
		}
	}
	return str
}
