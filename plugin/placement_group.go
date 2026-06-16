// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Compute::PlacementGroup handler.
//
// Mirrors the per-resource layout established by server.go: a properties
// struct with json tags, a parser, an observed-state mapper, a handler
// type implementing resourceHandler, and an init() that self-registers.
// hcloud placement-group create and delete are synchronous (no Action to
// poll), so those CRUD methods return Success immediately.

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

// PlacementGroupResourceType is the formae resource type for a Hetzner Cloud
// placement group.
const PlacementGroupResourceType = "HETZNER::Compute::PlacementGroup"

func init() {
	register(PlacementGroupResourceType, placementGroupHandler{})
}

// PlacementGroupProperties is the desired/observed state of a placement group.
type PlacementGroupProperties struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Labels map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID      int64   `json:"id,omitempty"`
	Servers []int64 `json:"servers,omitempty"`
}

func parsePlacementGroupProperties(data json.RawMessage) (*PlacementGroupProperties, error) {
	var p PlacementGroupProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid placement group properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("placement group properties missing 'name'")
	}
	if p.Type == "" {
		return nil, errors.New("placement group properties missing 'type'")
	}
	if p.Type != string(hcloud.PlacementGroupTypeSpread) {
		return nil, fmt.Errorf("placement group type must be %q", hcloud.PlacementGroupTypeSpread)
	}
	return &p, nil
}

// placementGroupHandler implements resourceHandler for placement groups.
type placementGroupHandler struct{}

func (placementGroupHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parsePlacementGroupProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.PlacementGroupCreateOpts{
		Name:   props.Name,
		Type:   hcloud.PlacementGroupType(props.Type),
		Labels: labels,
	}

	res, _, err := c.PlacementGroup().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// hcloud placement-group create is synchronous: no Action to poll.
	pg := res.PlacementGroup
	if pg == nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", "create returned no placement group", resource.OperationErrorCodeInternalFailure)}, nil
	}
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(pg.ID, 10), "")
	b, _ := json.Marshal(placementGroupPropertiesFrom(pg))
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (placementGroupHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	pg, _, err := c.PlacementGroup().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if pg == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := placementGroupPropertiesFrom(pg)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

func (placementGroupHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	desired, err := parsePlacementGroupProperties(req.DesiredProperties)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.PlacementGroupUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.PlacementGroup().Update(ctx, &hcloud.PlacementGroup{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	pg, _, err := c.PlacementGroup().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if pg == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "placement group not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(placementGroupPropertiesFrom(pg))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (placementGroupHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	pg, _, err := c.PlacementGroup().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if pg == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// hcloud placement-group delete is synchronous: no Action to poll.
	if _, err := c.PlacementGroup().Delete(ctx, pg); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (placementGroupHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	groups, err := c.PlacementGroup().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, strconv.FormatInt(g.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func placementGroupPropertiesFrom(pg *hcloud.PlacementGroup) PlacementGroupProperties {
	return PlacementGroupProperties{
		Name:    pg.Name,
		Type:    string(pg.Type),
		Labels:  stripManagedLabel(pg.Labels),
		ID:      pg.ID,
		Servers: pg.Servers,
	}
}
