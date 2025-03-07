// /*
// Copyright The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// */

// Code generated by client-gen. DO NOT EDIT.
package backendaddresspoolclient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/tracing"
	armnetwork "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/metrics"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/utils"
)

type Client struct {
	*armnetwork.LoadBalancerBackendAddressPoolsClient
	subscriptionID string
	tracer         tracing.Tracer
}

func New(subscriptionID string, credential azcore.TokenCredential, options *arm.ClientOptions) (Interface, error) {
	if options == nil {
		options = utils.GetDefaultOption()
	}
	tr := options.TracingProvider.NewTracer(utils.ModuleName, utils.ModuleVersion)

	client, err := armnetwork.NewLoadBalancerBackendAddressPoolsClient(subscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	return &Client{
		LoadBalancerBackendAddressPoolsClient: client,
		subscriptionID:                        subscriptionID,
		tracer:                                tr,
	}, nil
}

const GetOperationName = "LoadBalancerBackendAddressPoolsClient.Get"

// Get gets the BackendAddressPool
func (client *Client) Get(ctx context.Context, resourceGroupName string, loadbalancerName string, backendaddresspoolName string) (result *armnetwork.BackendAddressPool, err error) {

	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "BackendAddressPool", "get")
	defer func() { metricsCtx.Observe(ctx, err) }()
	ctx, endSpan := runtime.StartSpan(ctx, GetOperationName, client.tracer, nil)
	defer endSpan(err)
	resp, err := client.LoadBalancerBackendAddressPoolsClient.Get(ctx, resourceGroupName, loadbalancerName, backendaddresspoolName, nil)
	if err != nil {
		return nil, err
	}
	//handle statuscode
	return &resp.BackendAddressPool, nil
}

const CreateOrUpdateOperationName = "LoadBalancerBackendAddressPoolsClient.Create"

// CreateOrUpdate creates or updates a BackendAddressPool.
func (client *Client) CreateOrUpdate(ctx context.Context, resourceGroupName string, loadbalancerName string, backendaddresspoolName string, resource armnetwork.BackendAddressPool) (result *armnetwork.BackendAddressPool, err error) {
	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "BackendAddressPool", "create_or_update")
	defer func() { metricsCtx.Observe(ctx, err) }()
	ctx, endSpan := runtime.StartSpan(ctx, CreateOrUpdateOperationName, client.tracer, nil)
	defer endSpan(err)
	resp, err := utils.NewPollerWrapper(client.LoadBalancerBackendAddressPoolsClient.BeginCreateOrUpdate(ctx, resourceGroupName, loadbalancerName, backendaddresspoolName, resource, nil)).WaitforPollerResp(ctx)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return &resp.BackendAddressPool, nil
	}
	return nil, nil
}

const DeleteOperationName = "LoadBalancerBackendAddressPoolsClient.Delete"

// Delete deletes a BackendAddressPool by name.
func (client *Client) Delete(ctx context.Context, resourceGroupName string, loadbalancerName string, backendaddresspoolName string) (err error) {
	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "BackendAddressPool", "delete")
	defer func() { metricsCtx.Observe(ctx, err) }()
	ctx, endSpan := runtime.StartSpan(ctx, DeleteOperationName, client.tracer, nil)
	defer endSpan(err)
	_, err = utils.NewPollerWrapper(client.BeginDelete(ctx, resourceGroupName, loadbalancerName, backendaddresspoolName, nil)).WaitforPollerResp(ctx)
	return err
}

const ListOperationName = "LoadBalancerBackendAddressPoolsClient.List"

// List gets a list of BackendAddressPool in the resource group.
func (client *Client) List(ctx context.Context, resourceGroupName string, loadbalancerName string) (result []*armnetwork.BackendAddressPool, err error) {
	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "BackendAddressPool", "list")
	defer func() { metricsCtx.Observe(ctx, err) }()
	ctx, endSpan := runtime.StartSpan(ctx, ListOperationName, client.tracer, nil)
	defer endSpan(err)
	pager := client.LoadBalancerBackendAddressPoolsClient.NewListPager(resourceGroupName, loadbalancerName, nil)
	for pager.More() {
		nextResult, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, nextResult.Value...)
	}
	return result, nil
}
