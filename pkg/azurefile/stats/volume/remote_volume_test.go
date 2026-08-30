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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRemoteVolumeList(t *testing.T) {
	azureFilePV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-dynamic"},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-dynamic",
				Namespace: "default",
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       azureFileCSIDriverName,
					VolumeHandle: "rg#account#share###namespace",
					VolumeAttributes: map[string]string{
						resourceGroupAttribute:  "ignored-rg",
						storageAccountAttribute: "ignored-account",
						shareNameAttribute:      "ignored-share",
					},
				},
			},
		},
	}
	staticPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-static"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       azureFileCSIDriverName,
					VolumeHandle: "static-volume",
					VolumeAttributes: map[string]string{
						"RESOURCEGROUP":  "static-rg",
						"storageaccount": "static-account",
						"ShareName":      "static-share",
					},
				},
			},
		},
	}
	otherPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-other"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "other.csi.example.com",
					VolumeHandle: "other-rg#other-account#other-share",
				},
			},
		},
	}
	nonCSIPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-non-csi"},
	}

	kubeClient := fake.NewSimpleClientset(azureFilePV, staticPV, otherPV, nonCSIPV)

	volumes, err := NewRemoteVolume(kubeClient).List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(volumes) != 2 {
		t.Fatalf("List() returned %d volumes, want 2", len(volumes))
	}

	byName := make(map[string]Metadata, len(volumes))
	for _, volume := range volumes {
		byName[volume.PVName] = volume
	}

	dynamic := byName["pv-dynamic"]
	if dynamic.PVCName != "pvc-dynamic" || dynamic.PVCNamespace != "default" ||
		dynamic.ProvisionerName != azureFileCSIDriverName ||
		dynamic.StorageAccountResourceGroup != "rg" ||
		dynamic.StorageAccountName != "account" ||
		dynamic.ShareName != "share" {
		t.Errorf("unexpected dynamic volume metadata: %+v", dynamic)
	}

	static := byName["pv-static"]
	if static.StorageAccountResourceGroup != "static-rg" ||
		static.StorageAccountName != "static-account" ||
		static.ShareName != "static-share" {
		t.Errorf("unexpected static volume metadata: %+v", static)
	}
}

func TestRemoteVolumeListNilClient(t *testing.T) {
	if _, err := NewRemoteVolume(nil).List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want nil-client error")
	}
}
