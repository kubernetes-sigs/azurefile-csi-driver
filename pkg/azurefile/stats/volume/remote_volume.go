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

package volume

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	azureFileCSIDriverName = "file.csi.azure.com"

	resourceGroupAttribute  = "resourceGroup"
	storageAccountAttribute = "storageAccount"
	shareNameAttribute      = "shareName"
)

type RemoteVolume struct {
	client kubernetes.Interface
}

func NewRemoteVolume(client kubernetes.Interface) *RemoteVolume {
	return &RemoteVolume{
		client: client,
	}
}

func (r *RemoteVolume) List(ctx context.Context) (MetadataList, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("kubernetes client is nil")
	}

	persistentVolumes, err := r.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list persistent volumes: %w", err)
	}

	metadata := make(MetadataList, 0, len(persistentVolumes.Items))
	for i := range persistentVolumes.Items {
		persistentVolume := &persistentVolumes.Items[i]
		if persistentVolume.Spec.CSI == nil ||
			persistentVolume.Spec.CSI.Driver != azureFileCSIDriverName {
			continue
		}

		metadata = append(metadata, metadataFromPersistentVolume(persistentVolume))
	}

	return metadata, nil
}

func metadataFromPersistentVolume(persistentVolume *corev1.PersistentVolume) Metadata {
	csi := persistentVolume.Spec.CSI
	metadata := Metadata{
		PVName:          persistentVolume.Name,
		ProvisionerName: csi.Driver,
	}

	if persistentVolume.Spec.ClaimRef != nil {
		metadata.PVCName = persistentVolume.Spec.ClaimRef.Name
		metadata.PVCNamespace = persistentVolume.Spec.ClaimRef.Namespace
	}

	segments := strings.Split(csi.VolumeHandle, "#")
	if len(segments) >= 3 {
		metadata.StorageAccountResourceGroup = segments[0]
		metadata.StorageAccountName = segments[1]
		metadata.ShareName = segments[2]
	}

	// Static volumes may keep the Azure resource identity in volumeAttributes.
	// Prefer a non-empty volume-handle segment because it is the identity used
	// by the driver, and use attributes only to fill absent values.
	if metadata.StorageAccountResourceGroup == "" {
		metadata.StorageAccountResourceGroup = valueIgnoreCase(csi.VolumeAttributes, resourceGroupAttribute)
	}
	if metadata.StorageAccountName == "" {
		metadata.StorageAccountName = valueIgnoreCase(csi.VolumeAttributes, storageAccountAttribute)
	}
	if metadata.ShareName == "" {
		metadata.ShareName = valueIgnoreCase(csi.VolumeAttributes, shareNameAttribute)
	}

	return metadata
}

func valueIgnoreCase(values map[string]string, key string) string {
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}
