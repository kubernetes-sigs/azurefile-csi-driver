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
	"regexp"
	"strings"
)

var azureFileServerPattern = regexp.MustCompile(`^([a-z0-9]{3,24})(?:-secondary)?\.(?:` +
	`(?:privatelink\.)?file\.core\.(?:windows\.net|chinacloudapi\.cn|usgovcloudapi\.net|cloudapi\.de)|` +
	`z[1-9][0-9]?\.(?:privatelink\.)?(?:file|afs)\.storage\.azure\.net)$`)

// ParseCIFSTarget extracts the full server identity and file share from a UNC path.
func ParseCIFSTarget(device string) (string, string, bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(device), `\`, "/")
	parts := strings.Split(strings.Trim(normalized, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}

	return normalizeAccountAndShare(parts[0], parts[1])
}

// ParseNFSTarget extracts an Azure storage account and file share from an NFS export.
func ParseNFSTarget(device string) (string, string, bool) {
	server, export, ok := strings.Cut(strings.TrimSpace(device), ":/")
	if !ok || strings.TrimSpace(server) == "" {
		return "", "", false
	}

	parts := strings.Split(strings.Trim(export, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}

	// Azure Files NFS exports use /<storage-account>/<share>.
	return normalizeAccountAndShare(parts[0], parts[1])
}

func storageAccountFromServer(server string) string {
	matches := azureFileServerPattern.FindStringSubmatch(strings.TrimSuffix(server, "."))
	if len(matches) == 0 {
		return ""
	}
	return matches[1]
}

func normalizeAccountAndShare(account, share string) (string, string, bool) {
	account = strings.ToLower(strings.TrimSpace(account))
	share = strings.ToLower(strings.Trim(strings.TrimSpace(share), `/\`))
	if account == "" || share == "" {
		return "", "", false
	}

	return account, share, true
}
