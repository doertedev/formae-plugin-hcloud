// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Network::PrimaryIP handler.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - Create returns (*PrimaryIPCreateResult, *Response, error). The Result
//     carries a *PrimaryIP and a *Action; the Action is populated only when
//     the create triggers an assignment (e.g. assigneeId is set), so the
//     handler reports InProgress when an Action is present and Success with
//     read-back otherwise.
//   - Delete returns (*Response, error) — no Action. Delete is synchronous.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// PrimaryIPResourceType is the formae resource type for a Hetzner Cloud
// Primary IP.
const PrimaryIPResourceType = "HETZNER::Network::PrimaryIP"

func init() {
	register(PrimaryIPResourceType, primaryIPHandler{})
}

// PrimaryIPProperties is the desired/observed state of an hcloud Primary IP.
type PrimaryIPProperties struct {
	Name string `json:"name"`

	Type         string `json:"type"`
	AssigneeType string `json:"assigneeType,omitempty"`
	AssigneeID   int64  `json:"assigneeId,omitempty"`
	Location     string `json:"location,omitempty"`
	AutoDelete   *bool  `json:"autoDelete,omitempty"`

	Labels map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID int64  `json:"id,omitempty"`
	IP string `json:"ip,omitempty"`
}

func parsePrimaryIPProperties(data json.RawMessage) (*PrimaryIPProperties, error) {
	var p PrimaryIPProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid primary ip properties: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("primary ip properties missing 'name'")
	}
	if p.Type != "ipv4" && p.Type != "ipv6" {
		return nil, fmt.Errorf("primary ip properties 'type' must be %q or %q", "ipv4", "ipv6")
	}
	return &p, nil
}

// primaryIPHandler implements resourceHandler for
// HETZNER::Network::PrimaryIP.
type primaryIPHandler struct{}

func (primaryIPHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parsePrimaryIPProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.PrimaryIPCreateOpts{
		Name:         props.Name,
		Type:         hcloud.PrimaryIPType(props.Type),
		AssigneeType: props.AssigneeType,
		Labels:       labels,
		Location:     props.Location,
	}
	if props.AssigneeID != 0 {
		assignee := props.AssigneeID
		opts.AssigneeID = &assignee
	}
	if props.AutoDelete != nil {
		auto := *props.AutoDelete
		opts.AutoDelete = &auto
	}

	res, _, err := c.PrimaryIP().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// PrimaryIPCreateResult.Action is set only when an assignment action was
	// triggered; otherwise the create is synchronous.
	if res.Action != nil {
		return &resource.CreateResult{ProgressResult: progress(
			resource.OperationCreate, resource.OperationStatusInProgress,
			strconv.FormatInt(res.PrimaryIP.ID, 10), strconv.FormatInt(res.Action.ID, 10),
		)}, nil
	}
	b, _ := json.Marshal(primaryIPFrom(res.PrimaryIP))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(res.PrimaryIP.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (primaryIPHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	pip, _, err := c.PrimaryIP().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if pip == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := primaryIPFrom(pip)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (autoDelete, labels). PrimaryIPUpdateOpts
// also exposes Name, but this handler only forwards autoDelete and labels to
// match the documented mutable surface.
func (primaryIPHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired PrimaryIPProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid primary ip properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.PrimaryIPUpdateOpts{}
	if desired.AutoDelete != nil {
		auto := *desired.AutoDelete
		opts.AutoDelete = &auto
	}
	if desired.Labels != nil {
		labels := mergeManagedLabels(desired.Labels)
		opts.Labels = &labels
	}
	if opts.AutoDelete != nil || opts.Labels != nil {
		if _, _, err := c.PrimaryIP().Update(ctx, &hcloud.PrimaryIP{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	pip, _, err := c.PrimaryIP().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if pip == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "primary ip not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(primaryIPFrom(pip))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (primaryIPHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	pip, _, err := c.PrimaryIP().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if pip == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// IPrimaryIPClient.Delete is synchronous — no Action returned.
	if _, err := c.PrimaryIP().Delete(ctx, pip); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (primaryIPHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	pips, err := c.PrimaryIP().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(pips))
	for _, pip := range pips {
		ids = append(ids, strconv.FormatInt(pip.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func primaryIPFrom(p *hcloud.PrimaryIP) PrimaryIPProperties {
	ad := p.AutoDelete
	props := PrimaryIPProperties{
		Name:         p.Name,
		Type:         string(p.Type),
		AssigneeType: p.AssigneeType,
		AssigneeID:   p.AssigneeID,
		AutoDelete:   &ad,
		Labels:       stripManagedLabel(p.Labels),
		ID:           p.ID,
	}
	if p.Location != nil {
		props.Location = p.Location.Name
	}
	if p.IP != nil {
		props.IP = p.IP.String()
	}
	return props
}
