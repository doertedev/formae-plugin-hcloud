// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Storage::Volume handler.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - IVolumeClient.Create returns (VolumeCreateResult, *Response, error).
//     VolumeCreateResult.Action is populated only when the volume is attached
//     to a server at create time, so the handler reports InProgress with the
//     Action ID when present and Success with read-back otherwise.
//   - IVolumeClient.Delete returns (*Response, error). Delete is synchronous.

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

// VolumeResourceType is the formae resource type for a Hetzner Cloud volume.
const VolumeResourceType = "HETZNER::Storage::Volume"

func init() {
	register(VolumeResourceType, volumeHandler{})
}

// VolumeProperties is the desired/observed state of an hcloud volume.
type VolumeProperties struct {
	Name      string            `json:"name"`
	Size      int               `json:"size"`
	Server    int64             `json:"server,omitempty"`
	Location  string            `json:"location,omitempty"`
	Format    string            `json:"format,omitempty"`
	AutoMount *bool             `json:"autoMount,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID          int64  `json:"id,omitempty"`
	LinuxDevice string `json:"linuxDevice,omitempty"`
}

func parseVolumeProperties(data json.RawMessage) (*VolumeProperties, error) {
	var p VolumeProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid volume properties: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("volume properties missing 'name'")
	}
	if p.Size <= 0 {
		return nil, errors.New("volume properties 'size' must be positive")
	}
	// hcloud requires at least one of server/location to place the volume.
	if p.Server == 0 && p.Location == "" {
		return nil, errors.New("volume properties require 'server' or 'location'")
	}
	return &p, nil
}

// volumeHandler implements resourceHandler for HETZNER::Storage::Volume.
type volumeHandler struct{}

func (volumeHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseVolumeProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	labels := mergeManagedLabels(props.Labels)

	opts := hcloud.VolumeCreateOpts{
		Name:   props.Name,
		Size:   props.Size,
		Labels: labels,
	}
	if props.Server != 0 {
		opts.Server = &hcloud.Server{ID: props.Server}
	}
	if props.Location != "" {
		opts.Location = &hcloud.Location{Name: props.Location}
	}
	if props.Format != "" {
		format := props.Format
		opts.Format = &format
	}
	if props.AutoMount != nil {
		auto := *props.AutoMount
		opts.Automount = &auto
	}

	res, _, err := c.Volume().Create(ctx, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	// When attached to a server at create time hcloud returns an Action that
	// formae polls; otherwise the create is synchronous.
	if res.Action != nil {
		return &resource.CreateResult{ProgressResult: progress(
			resource.OperationCreate, resource.OperationStatusInProgress,
			strconv.FormatInt(res.Volume.ID, 10), strconv.FormatInt(res.Action.ID, 10),
		)}, nil
	}
	b, _ := json.Marshal(volumeFrom(res.Volume))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(res.Volume.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (volumeHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	vol, _, err := c.Volume().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if vol == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := volumeFrom(vol)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

// update changes mutable fields (name, labels). Size, server, location,
// format, and autoMount are immutable after creation in this handler.
func (volumeHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired VolumeProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid volume properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.VolumeUpdateOpts{}
	if desired.Name != "" {
		opts.Name = desired.Name
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}
	if opts.Name != "" || opts.Labels != nil {
		if _, _, err := c.Volume().Update(ctx, &hcloud.Volume{ID: id}, opts); err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
	}
	vol, _, err := c.Volume().GetByID(ctx, id)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
	}
	if vol == nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "volume not found", resource.OperationErrorCodeNotFound)}, nil
	}
	b, _ := json.Marshal(volumeFrom(vol))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (volumeHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	vol, _, err := c.Volume().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if vol == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// IVolumeClient.Delete is synchronous — no Action to poll.
	if _, err := c.Volume().Delete(ctx, vol); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (volumeHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	vols, err := c.Volume().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(vols))
	for _, v := range vols {
		ids = append(ids, strconv.FormatInt(v.ID, 10))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func volumeFrom(v *hcloud.Volume) VolumeProperties {
	props := VolumeProperties{
		Name:        v.Name,
		Size:        v.Size,
		Labels:      stripManagedLabel(v.Labels),
		ID:          v.ID,
		LinuxDevice: v.LinuxDevice,
	}
	if v.Server != nil {
		props.Server = v.Server.ID
	}
	if v.Location != nil {
		props.Location = v.Location.Name
	}
	if v.Format != nil {
		props.Format = *v.Format
	}
	return props
}
