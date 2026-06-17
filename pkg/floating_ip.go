// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Network::FloatingIP handler.
//
// hcloud-go's FloatingIP.Create returns a FloatingIPCreateResult whose Action
// is set only when a Server is assigned at create time. The handler reports
// InProgress with the Action ID when present, otherwise Success with a
// read-back (no assignment → synchronous response). Delete is synchronous in
// hcloud-go (no Action returned), so the handler reports Success directly.

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

// FloatingIPResourceType is the formae resource type for a Hetzner Cloud
// Floating IP.
const FloatingIPResourceType = "HETZNER::Network::FloatingIP"

func init() {
	register(FloatingIPResourceType, floatingIPHandler{})
}

// FloatingIPProperties is the desired/observed state of an hcloud Floating IP.
type FloatingIPProperties struct {
	Type         string            `json:"type"`
	Description  string            `json:"description,omitempty"`
	HomeLocation string            `json:"homeLocation,omitempty"`
	Server       int64             `json:"server,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID int64  `json:"id,omitempty"`
	IP string `json:"ip,omitempty"`
}

func parseFloatingIPProperties(data json.RawMessage) (*FloatingIPProperties, error) {
	var p FloatingIPProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid floating ip properties: %w", err)
	}
	if p.Type != "ipv4" && p.Type != "ipv6" {
		return nil, fmt.Errorf("floating ip properties 'type' must be %q or %q", "ipv4", "ipv6")
	}
	if p.HomeLocation == "" && p.Server == 0 {
		return nil, errors.New("floating ip properties require 'homeLocation' or 'server'")
	}
	return &p, nil
}

// floatingIPHandler implements resourceHandler for
// HETZNER::Network::FloatingIP.
type floatingIPHandler struct{}

func (floatingIPHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseFloatingIPProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.FloatingIPCreateOpts{
		Type:   hcloud.FloatingIPType(props.Type),
		Labels: labels,
	}
	if props.Description != "" {
		desc := props.Description
		opts.Description = &desc
	}
	if props.HomeLocation != "" {
		opts.HomeLocation = &hcloud.Location{Name: props.HomeLocation}
	}
	if props.Server != 0 {
		opts.Server = &hcloud.Server{ID: props.Server}
	}

	res, _, err := c.FloatingIP().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// When a Server is assigned at create time hcloud returns an Action that
	// formae polls; otherwise the create is synchronous.
	if res.Action != nil {
		return &resource.CreateResult{ProgressResult: progress(
			resource.OperationCreate, resource.OperationStatusInProgress,
			strconv.FormatInt(res.FloatingIP.ID, 10), strconv.FormatInt(res.Action.ID, 10),
		)}, nil
	}
	b, _ := json.Marshal(floatingIPFrom(res.FloatingIP))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(res.FloatingIP.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (floatingIPHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	fip, _, err := c.FloatingIP().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if fip == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := floatingIPFrom(fip)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (description, labels). Type, home location,
// and server assignment are immutable after creation in this handler.
func (floatingIPHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired FloatingIPProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid floating ip properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.FloatingIPUpdateOpts{}
	if desired.Description != "" {
		opts.Description = desired.Description
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Description != "" || opts.Labels != nil {
		if _, _, err := c.FloatingIP().Update(ctx, &hcloud.FloatingIP{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	fip, _, err := c.FloatingIP().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if fip == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "floating ip not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(floatingIPFrom(fip))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (floatingIPHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	fip, _, err := c.FloatingIP().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if fip == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// IFloatingIPClient.Delete is synchronous — no Action returned.
	if _, err := c.FloatingIP().Delete(ctx, fip); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (floatingIPHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	fips, err := c.FloatingIP().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(fips))
	for _, fip := range fips {
		ids = append(ids, strconv.FormatInt(fip.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func floatingIPFrom(f *hcloud.FloatingIP) FloatingIPProperties {
	props := FloatingIPProperties{
		Type:        string(f.Type),
		Description: f.Description,
		Labels:      stripManagedLabel(f.Labels),
		ID:          f.ID,
	}
	if f.HomeLocation != nil {
		props.HomeLocation = f.HomeLocation.Name
	}
	if f.Server != nil {
		props.Server = f.Server.ID
	}
	if f.IP != nil {
		props.IP = f.IP.String()
	}
	return props
}
