// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Network::LoadBalancer handler.
//
// Self-registers via init() — no shared file needs to change to add this
// resource. Mirrors the layout of server.go: properties struct, parser,
// observed-state mapper, handler, init/register.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// LoadBalancerResourceType is the formae resource type for a Hetzner Cloud
// Load Balancer.
const LoadBalancerResourceType = "HETZNER::Network::LoadBalancer"

func init() {
	register(LoadBalancerResourceType, loadBalancerHandler{})
}

// LoadBalancerProperties is the desired/observed state of an hcloud
// Load Balancer.
type LoadBalancerProperties struct {
	Name             string `json:"name"`
	LoadBalancerType string `json:"loadBalancerType"`

	// createOnly — set at create time, immutable afterwards.
	Location    string            `json:"location,omitempty"`
	NetworkZone string            `json:"networkZone,omitempty"`
	Algorithm   string            `json:"algorithm,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID   int64  `json:"id,omitempty"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

func parseLoadBalancerProperties(data json.RawMessage) (*LoadBalancerProperties, error) {
	var p LoadBalancerProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid load balancer properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("load balancer properties missing 'name'")
	}
	if p.LoadBalancerType == "" {
		return nil, errors.New("load balancer properties missing 'loadBalancerType'")
	}
	return &p, nil
}

// loadBalancerHandler implements resourceHandler for
// HETZNER::Network::LoadBalancer.
type loadBalancerHandler struct{}

func (loadBalancerHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseLoadBalancerProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.LoadBalancerCreateOpts{
		Name:             props.Name,
		LoadBalancerType: &hcloud.LoadBalancerType{Name: props.LoadBalancerType},
		Labels:           labels,
	}
	if props.Location != "" {
		opts.Location = &hcloud.Location{Name: props.Location}
	}
	if props.NetworkZone != "" {
		opts.NetworkZone = hcloud.NetworkZone(props.NetworkZone)
	}
	if props.Algorithm != "" {
		opts.Algorithm = &hcloud.LoadBalancerAlgorithm{Type: hcloud.LoadBalancerAlgorithmType(props.Algorithm)}
	}

	res, _, err := c.LoadBalancer().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// hcloud Load Balancer create returns a LoadBalancerCreateResult with an
	// Action; formae polls Status until the resource is ready.
	return &resource.CreateResult{ProgressResult: progress(
		resource.OperationCreate, resource.OperationStatusInProgress,
		strconv.FormatInt(res.LoadBalancer.ID, 10), strconv.FormatInt(res.Action.ID, 10),
	)}, nil
}

func (loadBalancerHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	lb, _, err := c.LoadBalancer().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if lb == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := loadBalancerPropertiesFrom(lb)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). Load Balancer type, location,
// network zone, and algorithm are immutable after creation in this handler.
func (loadBalancerHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	desired, err := parseLoadBalancerProperties(req.DesiredProperties)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.LoadBalancerUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.LoadBalancer().Update(ctx, &hcloud.LoadBalancer{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	lb, _, err := c.LoadBalancer().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if lb == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "load balancer not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(loadBalancerPropertiesFrom(lb))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (loadBalancerHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	lb, _, err := c.LoadBalancer().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if lb == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// hcloud Load Balancer Delete returns only (*Response, error) — there is
	// no Action to poll, so the operation is treated as synchronous.
	if _, err := c.LoadBalancer().Delete(ctx, lb); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (loadBalancerHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	lbs, err := c.LoadBalancer().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(lbs))
	for _, lb := range lbs {
		ids = append(ids, strconv.FormatInt(lb.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func loadBalancerPropertiesFrom(lb *hcloud.LoadBalancer) LoadBalancerProperties {
	props := LoadBalancerProperties{
		Name:             lb.Name,
		LoadBalancerType: lb.LoadBalancerType.Name,
		Algorithm:        string(lb.Algorithm.Type),
		Labels:           stripManagedLabel(lb.Labels),
		ID:               lb.ID,
	}
	if lb.Location != nil {
		props.Location = lb.Location.Name
	}
	if lb.PublicNet.IPv4.IP != nil {
		props.IPv4 = lb.PublicNet.IPv4.IP.String()
	}
	if lb.PublicNet.IPv6.IP != nil {
		props.IPv6 = lb.PublicNet.IPv6.IP.String()
	}
	return props
}
