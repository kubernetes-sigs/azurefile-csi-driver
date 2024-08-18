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

package credentials

import (
	"bytes"
	"os"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
)

func TestCreateAzureCredentialFileOnAzureChinaCloud(t *testing.T) {
	t.Run("WithEnvironmentVariables", func(t *testing.T) {
		os.Setenv(tenantIDChinaEnvVar, "test-tenant-id")
		os.Setenv(subscriptionIDChinaEnvVar, "test-subscription-id")
		os.Setenv(aadClientIDChinaEnvVar, "test-aad-client-id")
		os.Setenv(aadClientSecretChinaEnvVar, "test-aad-client-secret")
		os.Setenv(resourceGroupChinaEnvVar, "test-resource-group")
		os.Setenv(locationChinaEnvVar, "test-location")
		os.Setenv(federatedTokenFileVar, "/tmp/federated-token-file")
		withEnvironmentVariables(t, true)
	})
}

func TestCreateAzureCredentialFileOnAzurePublicCloud(t *testing.T) {
	t.Run("WithEnvironmentVariables", func(t *testing.T) {
		os.Setenv(tenantIDEnvVar, "test-tenant-id")
		os.Setenv(subscriptionIDEnvVar, "test-subscription-id")
		os.Setenv(aadClientIDEnvVar, "test-aad-client-id")
		os.Setenv(aadClientSecretEnvVar, "test-aad-client-secret")
		os.Setenv(resourceGroupEnvVar, "test-resource-group")
		os.Setenv(locationEnvVar, "test-location")
		os.Setenv(federatedTokenFileVar, "/tmp/federated-token-file")
		withEnvironmentVariables(t, false)
	})
}

func withEnvironmentVariables(t *testing.T, isAzureChinaCloud bool) {
	creds, err := CreateAzureCredentialFile(isAzureChinaCloud)
	defer func() {
		err := DeleteAzureCredentialFile()
		assert.NoError(t, err)
	}()
	assert.NoError(t, err)

	var cloud string
	if isAzureChinaCloud {
		cloud = AzureChinaCloud
	} else {
		cloud = AzurePublicCloud
	}

	assert.Equal(t, cloud, creds.Cloud)
	assert.Equal(t, "test-tenant-id", creds.TenantID)
	assert.Equal(t, "test-subscription-id", creds.SubscriptionID)
	assert.Equal(t, "test-aad-client-id", creds.AADClientID)
	assert.Equal(t, "test-aad-client-secret", creds.AADClientSecret)
	assert.Equal(t, "test-resource-group", creds.ResourceGroup)
	assert.Equal(t, "test-location", creds.Location)
	assert.Equal(t, "/tmp/federated-token-file", creds.AADFederatedTokenFile)

	azureCredentialFileContent, err := os.ReadFile(TempAzureCredentialFilePath)
	assert.NoError(t, err)

	const expectedAzureCredentialFileContent = `
	{
		"cloud": "{{.Cloud}}",
		"tenantId": "test-tenant-id",
		"subscriptionId": "test-subscription-id",
		"aadClientId": "test-aad-client-id",
		"aadClientSecret": "test-aad-client-secret",
		"resourceGroup": "test-resource-group",
		"location": "test-location",
		"aadFederatedTokenFile": "/tmp/federated-token-file",
		"cloudProviderBackoff": true,
		"cloudProviderBackoffRetries": 6,
		"cloudProviderBackoffDuration": 5
	}
	`
	tmpl := template.New("expectedAzureCredentialFileContent")
	tmpl, err = tmpl.Parse(expectedAzureCredentialFileContent)
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, struct {
		Cloud string
	}{
		cloud,
	})
	assert.NoError(t, err)
	assert.JSONEq(t, buf.String(), string(azureCredentialFileContent))
}
