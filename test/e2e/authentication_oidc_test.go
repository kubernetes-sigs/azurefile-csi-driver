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

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/azurefile-csi-driver/test/utils/credentials"
)

// The 1.35 suite already provides these CAPZ OIDC helpers; 1.34 needs them for WI-key mounts.
var authenticationOIDCReady bool

func prepareAuthenticationOIDC(ctx context.Context, cs clientset.Interface, creds *credentials.Credentials) error {
	if authenticationOIDCReady {
		return nil
	}
	issuer, err := discoverOIDCIssuer(ctx, cs)
	if err != nil {
		return err
	}
	if err := waitForOIDCJWKS(issuer, 5*time.Minute); err != nil {
		return err
	}
	if err := verifyJWKSKeyMatch(ctx, cs, issuer, creds); err != nil {
		return err
	}
	authenticationOIDCReady = true
	return nil
}

func discoverOIDCIssuer(ctx context.Context, cs clientset.Interface) (string, error) {
	body, err := cs.Discovery().RESTClient().Get().AbsPath("/.well-known/openid-configuration").Do(ctx).Raw()
	if err != nil {
		return "", fmt.Errorf("failed to get OIDC configuration: %w", err)
	}
	var config struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		return "", fmt.Errorf("failed to parse OIDC configuration: %w", err)
	}
	if config.Issuer == "" {
		return "", fmt.Errorf("OIDC issuer URL is empty in cluster configuration")
	}
	return config.Issuer, nil
}

func waitForOIDCJWKS(issuerURL string, timeout time.Duration) error {
	jwksURL := strings.TrimSuffix(issuerURL, "/") + "/openid/v1/jwks"
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(jwksURL) //nolint:gosec // URL comes from the cluster's OIDC configuration.
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case readErr != nil:
				lastErr = readErr
			case resp.StatusCode != http.StatusOK:
				lastErr = fmt.Errorf("JWKS returned HTTP %d", resp.StatusCode)
			default:
				kids, parseErr := extractJWKSKids(body)
				if parseErr == nil && len(kids) > 0 {
					return nil
				}
				lastErr = parseErr
				if lastErr == nil {
					lastErr = fmt.Errorf("JWKS contains no signing keys")
				}
			}
		}
		log.Printf("Waiting for OIDC JWKS at %s: %v", jwksURL, lastErr)
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timed out waiting for OIDC JWKS: %w", lastErr)
}

func verifyJWKSKeyMatch(ctx context.Context, cs clientset.Interface, issuerURL string, creds *credentials.Credentials) error {
	tokenResp, err := cs.CoreV1().ServiceAccounts("default").CreateToken(ctx, "default", &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{Audiences: []string{"api://AzureADTokenExchange"}},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating SA token: %w", err)
	}
	tokenKid, err := extractJWTKid(tokenResp.Status.Token)
	if err != nil {
		return err
	}
	jwksURL := strings.TrimSuffix(issuerURL, "/") + "/openid/v1/jwks"
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(jwksURL) //nolint:gosec // URL comes from the cluster's OIDC configuration.
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS returned HTTP %d", resp.StatusCode)
	}
	kids, err := extractJWKSKids(body)
	if err != nil {
		return err
	}
	for _, kid := range kids {
		if kid == tokenKid {
			return nil
		}
	}
	log.Printf("CAPZ OIDC JWKS does not contain the cluster signing key %s; repairing the published keys", tokenKid)
	apiJWKS, err := cs.Discovery().RESTClient().Get().AbsPath("/openid/v1/jwks").Do(ctx).Raw()
	if err != nil {
		return fmt.Errorf("fetching cluster JWKS: %w", err)
	}
	apiKids, err := extractJWKSKids(apiJWKS)
	if err != nil {
		return err
	}
	found := false
	for _, kid := range apiKids {
		if kid == tokenKid {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("cluster JWKS does not contain token signing key %s", tokenKid)
	}
	_, blobURL, err := extractStorageInfoFromIssuer(issuerURL)
	if err != nil {
		return err
	}
	cred, err := getAzureCredential(creds)
	if err != nil {
		return err
	}
	if err := uploadAuthenticationOIDCBlob(ctx, blobURL, "openid/v1/jwks", apiJWKS, cred); err != nil {
		return fmt.Errorf("publishing corrected JWKS: %w", err)
	}
	config, err := json.Marshal(map[string]interface{}{
		"issuer": issuerURL, "jwks_uri": jwksURL, "response_types_supported": []string{"id_token"},
		"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
	})
	if err != nil {
		return err
	}
	if err := uploadAuthenticationOIDCBlob(ctx, blobURL, ".well-known/openid-configuration", config, cred); err != nil {
		return fmt.Errorf("publishing corrected OIDC configuration: %w", err)
	}
	return nil
}

func extractJWTKid(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("service account token is not a JWT")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decoding JWT header: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return "", err
	}
	if header.Kid == "" {
		return "", fmt.Errorf("JWT header has no signing key ID")
	}
	return header.Kid, nil
}

func extractJWKSKids(body []byte) ([]string, error) {
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("parsing JWKS: %w", err)
	}
	var kids []string
	for _, key := range jwks.Keys {
		if key.Kid != "" {
			kids = append(kids, key.Kid)
		}
	}
	return kids, nil
}

func extractStorageInfoFromIssuer(issuer string) (account, blobURL string, err error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(u.Hostname(), ".", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected CAPZ issuer hostname %s", u.Hostname())
	}
	index := strings.Index(parts[1], "web.")
	if index < 0 {
		return "", "", fmt.Errorf("CAPZ issuer does not use a storage static website: %s", u.Hostname())
	}
	suffix := parts[1][index+4:]
	switch suffix {
	case "core.windows.net", "core.chinacloudapi.cn", "core.usgovcloudapi.net":
	default:
		return "", "", fmt.Errorf("CAPZ issuer has an unexpected Azure Storage suffix: %s", suffix)
	}
	return parts[0], fmt.Sprintf("https://%s.blob.%s", parts[0], suffix), nil
}

func uploadAuthenticationOIDCBlob(ctx context.Context, blobURL, name string, payload []byte, credential azcore.TokenCredential) error {
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://storage.azure.com/.default"}})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, blobURL+"/$web/"+name, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-ms-blob-type", "BlockBlob")
	request.Header.Set("x-ms-version", "2023-11-03")
	request.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("uploading OIDC blob %s: HTTP %d, Azure error %s, request ID %s",
			name, response.StatusCode, response.Header.Get("x-ms-error-code"), response.Header.Get("x-ms-request-id"))
	}
	return nil
}

func getAzureCredential(creds *credentials.Credentials) (azcore.TokenCredential, error) {
	cloudConfig := cloud.AzurePublic
	switch strings.ToUpper(creds.Cloud) {
	case "AZURECHINACLOUD":
		cloudConfig = cloud.AzureChina
	case "AZUREUSGOVERNMENTCLOUD":
		cloudConfig = cloud.AzureGovernment
	}
	if host := os.Getenv("AZURE_AUTHORITY_HOST"); host != "" {
		cloudConfig.ActiveDirectoryAuthorityHost = host
	}
	options := azcore.ClientOptions{Cloud: cloudConfig}
	if creds.AADFederatedTokenFile != "" {
		return azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
			ClientOptions: options, TenantID: creds.TenantID, ClientID: creds.AADClientID, TokenFilePath: creds.AADFederatedTokenFile,
		})
	}
	return azidentity.NewClientSecretCredential(creds.TenantID, creds.AADClientID, creds.AADClientSecret,
		&azidentity.ClientSecretCredentialOptions{ClientOptions: options})
}

func TestAuthenticationOIDCSigningKeyMetadata(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"current-key"}`))
	kid, err := extractJWTKid(header + ".e30.signature")
	if err != nil {
		t.Fatal(err)
	}
	kids, err := extractJWKSKids([]byte(`{"keys":[{"kid":"previous-key","kty":"RSA"},{"kid":"current-key","kty":"RSA"},{}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if kid != "current-key" || len(kids) != 2 || kids[1] != kid {
		t.Fatalf("cluster signing key %q must be found among published rotation keys %v", kid, kids)
	}
}

func TestAuthenticationOIDCStorageEndpoints(t *testing.T) {
	for _, tc := range []struct {
		issuer string
		blob   string
	}{
		{"https://oidcaccount.z13.web.core.windows.net/", "https://oidcaccount.blob.core.windows.net"},
		{"https://oidcaccount.z1.web.core.chinacloudapi.cn/", "https://oidcaccount.blob.core.chinacloudapi.cn"},
	} {
		t.Run(tc.issuer, func(t *testing.T) {
			account, blob, err := extractStorageInfoFromIssuer(tc.issuer)
			if err != nil {
				t.Fatal(err)
			}
			if account != "oidcaccount" || blob != tc.blob {
				t.Fatalf("issuer resolved to account %q, blob %q", account, blob)
			}
		})
	}
}

type authenticationBlobTestCredential struct{}

func (authenticationBlobTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fixture-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAuthenticationOIDCBlobPublication(t *testing.T) {
	payload := []byte(`{"keys":[{"kid":"current-key"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/$web/openid/v1/jwks" {
			t.Errorf("unexpected blob upload %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-token" ||
			request.Header.Get("x-ms-blob-type") != "BlockBlob" || request.Header.Get("x-ms-version") == "" ||
			request.Header.Get("x-ms-date") == "" || request.Header.Get("Content-Type") != "application/json" {
			t.Error("blob upload is missing Azure OAuth/BlockBlob headers")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Equal(body, payload) {
			t.Error("blob upload changed the JWKS document")
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	if err := uploadAuthenticationOIDCBlob(context.Background(), server.URL, "openid/v1/jwks", payload, authenticationBlobTestCredential{}); err != nil {
		t.Fatal(err)
	}
}
