//go:build go1.18
// +build go1.18

// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.
// Code generated by Microsoft (R) AutoRest Code Generator. DO NOT EDIT.
// Changes may cause incorrect behavior and will be lost if the code is regenerated.

package armnetwork

import (
	"context"
	"errors"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"net/http"
	"net/url"
	"strings"
)

// PrivateDNSZoneGroupsClient contains the methods for the PrivateDNSZoneGroups group.
// Don't use this type directly, use NewPrivateDNSZoneGroupsClient() instead.
type PrivateDNSZoneGroupsClient struct {
	internal       *arm.Client
	subscriptionID string
}

// NewPrivateDNSZoneGroupsClient creates a new instance of PrivateDNSZoneGroupsClient with the specified values.
//   - subscriptionID - The subscription credentials which uniquely identify the Microsoft Azure subscription. The subscription
//     ID forms part of the URI for every service call.
//   - credential - used to authorize requests. Usually a credential from azidentity.
//   - options - pass nil to accept the default values.
func NewPrivateDNSZoneGroupsClient(subscriptionID string, credential azcore.TokenCredential, options *arm.ClientOptions) (*PrivateDNSZoneGroupsClient, error) {
	cl, err := arm.NewClient(moduleName, moduleVersion, credential, options)
	if err != nil {
		return nil, err
	}
	client := &PrivateDNSZoneGroupsClient{
		subscriptionID: subscriptionID,
		internal:       cl,
	}
	return client, nil
}

// BeginCreateOrUpdate - Creates or updates a private dns zone group in the specified private endpoint.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2023-05-01
//   - resourceGroupName - The name of the resource group.
//   - privateEndpointName - The name of the private endpoint.
//   - privateDNSZoneGroupName - The name of the private dns zone group.
//   - parameters - Parameters supplied to the create or update private dns zone group operation.
//   - options - PrivateDNSZoneGroupsClientBeginCreateOrUpdateOptions contains the optional parameters for the PrivateDNSZoneGroupsClient.BeginCreateOrUpdate
//     method.
func (client *PrivateDNSZoneGroupsClient) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, parameters PrivateDNSZoneGroup, options *PrivateDNSZoneGroupsClientBeginCreateOrUpdateOptions) (*runtime.Poller[PrivateDNSZoneGroupsClientCreateOrUpdateResponse], error) {
	if options == nil || options.ResumeToken == "" {
		resp, err := client.createOrUpdate(ctx, resourceGroupName, privateEndpointName, privateDNSZoneGroupName, parameters, options)
		if err != nil {
			return nil, err
		}
		poller, err := runtime.NewPoller(resp, client.internal.Pipeline(), &runtime.NewPollerOptions[PrivateDNSZoneGroupsClientCreateOrUpdateResponse]{
			FinalStateVia: runtime.FinalStateViaAzureAsyncOp,
			Tracer:        client.internal.Tracer(),
		})
		return poller, err
	} else {
		return runtime.NewPollerFromResumeToken(options.ResumeToken, client.internal.Pipeline(), &runtime.NewPollerFromResumeTokenOptions[PrivateDNSZoneGroupsClientCreateOrUpdateResponse]{
			Tracer: client.internal.Tracer(),
		})
	}
}

// CreateOrUpdate - Creates or updates a private dns zone group in the specified private endpoint.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2023-05-01
func (client *PrivateDNSZoneGroupsClient) createOrUpdate(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, parameters PrivateDNSZoneGroup, options *PrivateDNSZoneGroupsClientBeginCreateOrUpdateOptions) (*http.Response, error) {
	var err error
	const operationName = "PrivateDNSZoneGroupsClient.BeginCreateOrUpdate"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.createOrUpdateCreateRequest(ctx, resourceGroupName, privateEndpointName, privateDNSZoneGroupName, parameters, options)
	if err != nil {
		return nil, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK, http.StatusCreated) {
		err = runtime.NewResponseError(httpResp)
		return nil, err
	}
	return httpResp, nil
}

// createOrUpdateCreateRequest creates the CreateOrUpdate request.
func (client *PrivateDNSZoneGroupsClient) createOrUpdateCreateRequest(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, parameters PrivateDNSZoneGroup, options *PrivateDNSZoneGroupsClientBeginCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/privateEndpoints/{privateEndpointName}/privateDnsZoneGroups/{privateDnsZoneGroupName}"
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if privateEndpointName == "" {
		return nil, errors.New("parameter privateEndpointName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateEndpointName}", url.PathEscape(privateEndpointName))
	if privateDNSZoneGroupName == "" {
		return nil, errors.New("parameter privateDNSZoneGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateDnsZoneGroupName}", url.PathEscape(privateDNSZoneGroupName))
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2023-05-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	if err := runtime.MarshalAsJSON(req, parameters); err != nil {
		return nil, err
	}
	return req, nil
}

// BeginDelete - Deletes the specified private dns zone group.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2023-05-01
//   - resourceGroupName - The name of the resource group.
//   - privateEndpointName - The name of the private endpoint.
//   - privateDNSZoneGroupName - The name of the private dns zone group.
//   - options - PrivateDNSZoneGroupsClientBeginDeleteOptions contains the optional parameters for the PrivateDNSZoneGroupsClient.BeginDelete
//     method.
func (client *PrivateDNSZoneGroupsClient) BeginDelete(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, options *PrivateDNSZoneGroupsClientBeginDeleteOptions) (*runtime.Poller[PrivateDNSZoneGroupsClientDeleteResponse], error) {
	if options == nil || options.ResumeToken == "" {
		resp, err := client.deleteOperation(ctx, resourceGroupName, privateEndpointName, privateDNSZoneGroupName, options)
		if err != nil {
			return nil, err
		}
		poller, err := runtime.NewPoller(resp, client.internal.Pipeline(), &runtime.NewPollerOptions[PrivateDNSZoneGroupsClientDeleteResponse]{
			FinalStateVia: runtime.FinalStateViaLocation,
			Tracer:        client.internal.Tracer(),
		})
		return poller, err
	} else {
		return runtime.NewPollerFromResumeToken(options.ResumeToken, client.internal.Pipeline(), &runtime.NewPollerFromResumeTokenOptions[PrivateDNSZoneGroupsClientDeleteResponse]{
			Tracer: client.internal.Tracer(),
		})
	}
}

// Delete - Deletes the specified private dns zone group.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2023-05-01
func (client *PrivateDNSZoneGroupsClient) deleteOperation(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, options *PrivateDNSZoneGroupsClientBeginDeleteOptions) (*http.Response, error) {
	var err error
	const operationName = "PrivateDNSZoneGroupsClient.BeginDelete"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.deleteCreateRequest(ctx, resourceGroupName, privateEndpointName, privateDNSZoneGroupName, options)
	if err != nil {
		return nil, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK, http.StatusAccepted, http.StatusNoContent) {
		err = runtime.NewResponseError(httpResp)
		return nil, err
	}
	return httpResp, nil
}

// deleteCreateRequest creates the Delete request.
func (client *PrivateDNSZoneGroupsClient) deleteCreateRequest(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, options *PrivateDNSZoneGroupsClientBeginDeleteOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/privateEndpoints/{privateEndpointName}/privateDnsZoneGroups/{privateDnsZoneGroupName}"
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if privateEndpointName == "" {
		return nil, errors.New("parameter privateEndpointName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateEndpointName}", url.PathEscape(privateEndpointName))
	if privateDNSZoneGroupName == "" {
		return nil, errors.New("parameter privateDNSZoneGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateDnsZoneGroupName}", url.PathEscape(privateDNSZoneGroupName))
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	req, err := runtime.NewRequest(ctx, http.MethodDelete, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2023-05-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// Get - Gets the private dns zone group resource by specified private dns zone group name.
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2023-05-01
//   - resourceGroupName - The name of the resource group.
//   - privateEndpointName - The name of the private endpoint.
//   - privateDNSZoneGroupName - The name of the private dns zone group.
//   - options - PrivateDNSZoneGroupsClientGetOptions contains the optional parameters for the PrivateDNSZoneGroupsClient.Get
//     method.
func (client *PrivateDNSZoneGroupsClient) Get(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, options *PrivateDNSZoneGroupsClientGetOptions) (PrivateDNSZoneGroupsClientGetResponse, error) {
	var err error
	const operationName = "PrivateDNSZoneGroupsClient.Get"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.getCreateRequest(ctx, resourceGroupName, privateEndpointName, privateDNSZoneGroupName, options)
	if err != nil {
		return PrivateDNSZoneGroupsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return PrivateDNSZoneGroupsClientGetResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK) {
		err = runtime.NewResponseError(httpResp)
		return PrivateDNSZoneGroupsClientGetResponse{}, err
	}
	resp, err := client.getHandleResponse(httpResp)
	return resp, err
}

// getCreateRequest creates the Get request.
func (client *PrivateDNSZoneGroupsClient) getCreateRequest(ctx context.Context, resourceGroupName string, privateEndpointName string, privateDNSZoneGroupName string, options *PrivateDNSZoneGroupsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/privateEndpoints/{privateEndpointName}/privateDnsZoneGroups/{privateDnsZoneGroupName}"
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if privateEndpointName == "" {
		return nil, errors.New("parameter privateEndpointName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateEndpointName}", url.PathEscape(privateEndpointName))
	if privateDNSZoneGroupName == "" {
		return nil, errors.New("parameter privateDNSZoneGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateDnsZoneGroupName}", url.PathEscape(privateDNSZoneGroupName))
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2023-05-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// getHandleResponse handles the Get response.
func (client *PrivateDNSZoneGroupsClient) getHandleResponse(resp *http.Response) (PrivateDNSZoneGroupsClientGetResponse, error) {
	result := PrivateDNSZoneGroupsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.PrivateDNSZoneGroup); err != nil {
		return PrivateDNSZoneGroupsClientGetResponse{}, err
	}
	return result, nil
}

// NewListPager - Gets all private dns zone groups in a private endpoint.
//
// Generated from API version 2023-05-01
//   - privateEndpointName - The name of the private endpoint.
//   - resourceGroupName - The name of the resource group.
//   - options - PrivateDNSZoneGroupsClientListOptions contains the optional parameters for the PrivateDNSZoneGroupsClient.NewListPager
//     method.
func (client *PrivateDNSZoneGroupsClient) NewListPager(privateEndpointName string, resourceGroupName string, options *PrivateDNSZoneGroupsClientListOptions) *runtime.Pager[PrivateDNSZoneGroupsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[PrivateDNSZoneGroupsClientListResponse]{
		More: func(page PrivateDNSZoneGroupsClientListResponse) bool {
			return page.NextLink != nil && len(*page.NextLink) > 0
		},
		Fetcher: func(ctx context.Context, page *PrivateDNSZoneGroupsClientListResponse) (PrivateDNSZoneGroupsClientListResponse, error) {
			ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, "PrivateDNSZoneGroupsClient.NewListPager")
			nextLink := ""
			if page != nil {
				nextLink = *page.NextLink
			}
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), nextLink, func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, privateEndpointName, resourceGroupName, options)
			}, nil)
			if err != nil {
				return PrivateDNSZoneGroupsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
		Tracer: client.internal.Tracer(),
	})
}

// listCreateRequest creates the List request.
func (client *PrivateDNSZoneGroupsClient) listCreateRequest(ctx context.Context, privateEndpointName string, resourceGroupName string, options *PrivateDNSZoneGroupsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/privateEndpoints/{privateEndpointName}/privateDnsZoneGroups"
	if privateEndpointName == "" {
		return nil, errors.New("parameter privateEndpointName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{privateEndpointName}", url.PathEscape(privateEndpointName))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2023-05-01")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// listHandleResponse handles the List response.
func (client *PrivateDNSZoneGroupsClient) listHandleResponse(resp *http.Response) (PrivateDNSZoneGroupsClientListResponse, error) {
	result := PrivateDNSZoneGroupsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.PrivateDNSZoneGroupListResult); err != nil {
		return PrivateDNSZoneGroupsClientListResponse{}, err
	}
	return result, nil
}
