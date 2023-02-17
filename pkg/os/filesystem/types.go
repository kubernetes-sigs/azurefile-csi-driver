/*
Copyright 2023 The Kubernetes Authors.

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

package filesystem

type PathExistsRequest struct {
	// The path whose existence we want to check in the host's filesystem
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
}

type MkdirRequest struct {
	// The path to create in the host's filesystem.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	// Non-existent parent directories in the path will be automatically created.
	// Directories will be created with Read and Write privileges of the Windows
	// User account under which csi-proxy is started (typically LocalSystem).
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// Depending on the context parameter of this function, the path prefix needs
	// to match the paths specified either as kubelet-csi-plugins-path
	// or as kubelet-pod-path parameters of csi-proxy.
	// The path parameter cannot already exist in the host's filesystem.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// Maximum path length will be capped to 260 characters.
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
}

type RmdirRequest struct {
	// The path to remove in the host's filesystem.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// Depending on the context parameter of this function, the path prefix needs
	// to match the paths specified either as kubelet-csi-plugins-path
	// or as kubelet-pod-path parameters of csi-proxy.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// Path cannot be a file of type symlink.
	// Maximum path length will be capped to 260 characters.
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	// Force remove all contents under path (if any).
	Force bool `protobuf:"varint,2,opt,name=force,proto3" json:"force,omitempty"`
}

type LinkPathRequest struct {
	// The path where the symlink is created in the host's filesystem.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// The path prefix needs to match the paths specified as
	// kubelet-csi-plugins-path parameter of csi-proxy.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// source_path cannot already exist in the host filesystem.
	// Maximum path length will be capped to 260 characters.
	SourcePath string `protobuf:"bytes,1,opt,name=source_path,json=sourcePath,proto3" json:"source_path,omitempty"`
	// Target path in the host's filesystem used for the symlink creation.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// The path prefix needs to match the paths specified as
	// kubelet-pod-path parameter of csi-proxy.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// target_path needs to exist as a directory in the host that is empty.
	// target_path cannot be a symbolic link.
	// Maximum path length will be capped to 260 characters.
	TargetPath string `protobuf:"bytes,2,opt,name=target_path,json=targetPath,proto3" json:"target_path,omitempty"`
}

type CreateSymlinkRequest struct {
	// The path of the existing directory to be linked.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// The path prefix needs needs to match the paths specified as
	// kubelet-csi-plugins-path parameter of csi-proxy.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// source_path cannot already exist in the host filesystem.
	// Maximum path length will be capped to 260 characters.
	SourcePath string `protobuf:"bytes,1,opt,name=source_path,json=sourcePath,proto3" json:"source_path,omitempty"`
	// Target path is the location of the new directory entry to be created in the host's filesystem.
	// All special characters allowed by Windows in path names will be allowed
	// except for restrictions noted below. For details, please check:
	// https://docs.microsoft.com/en-us/windows/win32/fileio/naming-a-file
	//
	// Restrictions:
	// Only absolute path (indicated by a drive letter prefix: e.g. "C:\") is accepted.
	// The path prefix needs to match the paths specified as
	// kubelet-pod-path parameter of csi-proxy.
	// UNC paths of the form "\\server\share\path\file" are not allowed.
	// All directory separators need to be backslash character: "\".
	// Characters: .. / : | ? * in the path are not allowed.
	// target_path needs to exist as a directory in the host that is empty.
	// target_path cannot be a symbolic link.
	// Maximum path length will be capped to 260 characters.
	TargetPath string `protobuf:"bytes,2,opt,name=target_path,json=targetPath,proto3" json:"target_path,omitempty"`
}

type IsMountPointRequest struct {
	// The path whose existence we want to check in the host's filesystem
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
}

type IsSymlinkRequest struct {
	// The path whose existence as a symlink we want to check in the host's filesystem.
	Path string `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
}
