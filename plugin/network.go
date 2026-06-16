// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Network::Network handler.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - INetworkClient.Create returns (*Network, *Response, error). The create
//     is synchronous and returns no Action, so the handler reports Success
//     with read-back.
//   - INetworkClient.Delete returns (*Response, error). Delete is synchronous.
//
// Subnets, routes, and server attachment are out of scope for this resource:
// the handler models the network object itself (name, ipRange, labels).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// NetworkResourceType is the formae resource type for a Hetzner Cloud network.
const NetworkResourceType = "HETZNER::Network::Network"

func init() {
	register(NetworkResourceType, networkHandler{})
}

// NetworkProperties is the desired/observed state of an hcloud network.
type NetworkProperties struct {
	Name    string            `json:"name"`
	IPRange string            `json:"ipRange"`
	Labels  map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID               int64 `json:"id,omitempty"`
	ExposeRoutesToV2 bool  `json:"exposeRoutesToV2,omitempty"`
}

func parseNetworkProperties(data json.RawMessage) (*NetworkProperties, error) {
	var p NetworkProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid network properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("network properties missing 'name'")
	}
	if p.IPRange == "" {
		return nil, errors.New("network properties missing 'ipRange'")
	}
	if _, _, err := net.ParseCIDR(p.IPRange); err != nil {
		return nil, fmt.Errorf("network properties 'ipRange' is not a valid CIDR: %w", err)
	}
	return &p, nil
}

// networkHandler implements resourceHandler for HETZNER::Network::Network.
type networkHandler struct{}

func (networkHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseNetworkProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	_, ipNet, err := net.ParseCIDR(props.IPRange)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.NetworkCreateOpts{
		Name:    props.Name,
		IPRange: ipNet,
		Labels:  labels,
	}

	// INetworkClient.Create is synchronous — no Action to poll.
	network, _, err := c.Network().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	b, _ := json.Marshal(networkFrom(network))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(network.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (networkHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	network, _, err := c.Network().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if network == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := networkFrom(network)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). IP range, subnets, and routes
// are out of scope for this handler.
func (networkHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired NetworkProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid network properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.NetworkUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.Network().Update(ctx, &hcloud.Network{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	network, _, err := c.Network().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if network == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "network not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(networkFrom(network))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (networkHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	network, _, err := c.Network().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if network == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// INetworkClient.Delete is synchronous — no Action to poll.
	if _, err := c.Network().Delete(ctx, network); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (networkHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	networks, err := c.Network().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(networks))
	for _, n := range networks {
		ids = append(ids, strconv.FormatInt(n.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func networkFrom(n *hcloud.Network) NetworkProperties {
	props := NetworkProperties{
		Name:             n.Name,
		Labels:           stripManagedLabel(n.Labels),
		ID:               n.ID,
		ExposeRoutesToV2: n.ExposeRoutesToVSwitch,
	}
	if n.IPRange != nil {
		props.IPRange = n.IPRange.String()
	}
	return props
}
