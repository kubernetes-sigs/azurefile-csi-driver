/*
Copyright 2017 The Kubernetes Authors.

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
	net "net"
	reflect "reflect"

	gomock "go.uber.org/mock/gomock"
)

// MockResolver is a mock of Resolver interface.
type MockResolver struct {
	ctrl     *gomock.Controller
	recorder *MockResolverMockRecorder
}

// MockResolverMockRecorder is the mock recorder for MockResolver.
type MockResolverMockRecorder struct {
	mock *MockResolver
}

// NewMockResolver creates a new mock instance.
func NewMockResolver(ctrl *gomock.Controller) *MockResolver {
	mock := &MockResolver{ctrl: ctrl}
	mock.recorder = &MockResolverMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockResolver) EXPECT() *MockResolverMockRecorder {
	return m.recorder
}

// ResolveIPAddr mocks base method.
func (m *MockResolver) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ResolveIPAddr", network, address)
	ret0, _ := ret[0].(*net.IPAddr)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ResolveIPAddr indicates an expected call of ResolveIPAddr.
func (mr *MockResolverMockRecorder) ResolveIPAddr(network, address interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ResolveIPAddr", reflect.TypeOf((*MockResolver)(nil).ResolveIPAddr), network, address)
}
