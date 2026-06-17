// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Compute::Image (snapshot) handler.
//
// hcloud images are mostly read-only (system/app images). formae only owns
// snapshots: creation (a snapshot of a server via Server().CreateImage) and
// deletion of an image you own. Create is async (returns an Action); delete
// is synchronous through the SDK (hcloud-go's Image().Delete does not surface
// an Action). Read/Update/Delete operate on the IMAGE id, which is also the
// resource's NativeID.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ImageResourceType is the formae resource type for a Hetzner Cloud snapshot
// image.
const ImageResourceType = "HETZNER::Compute::Image"

func init() {
	register(ImageResourceType, imageHandler{})
}

// ImageProperties is the desired/observed state of a snapshot image.
type ImageProperties struct {
	// Desired (create-time):
	Server      int64             `json:"server,omitempty"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`

	// Observed outputs:
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	ImageSize float64 `json:"imageSize,omitempty"`
	DiskSize  float64 `json:"diskSize,omitempty"`
	Created   string  `json:"created,omitempty"`
	Status    string  `json:"status,omitempty"`
}

func parseImageProperties(data json.RawMessage) (*ImageProperties, error) {
	var p ImageProperties
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid image properties: %w", err)
	}
	if p.Server == 0 {
		return nil, errors.New("image properties missing 'server'")
	}
	return &p, nil
}

// imageHandler implements resourceHandler for snapshot images.
type imageHandler struct{}

func (imageHandler) create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error) {
	props, err := parseImageProperties(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInvalidRequest)}, nil
	}

	// hcloud's Server.CreateImage can ONLY create snapshots; backups and
	// other image types are produced by a separate, automatic mechanism.
	// Validate the requested type so an unsupported value (e.g. "backup")
	// is not silently coerced into a snapshot.
	if props.Type != "" && props.Type != "snapshot" {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", fmt.Sprintf("image 'type' must be \"snapshot\" (got %q); hcloud only supports creating snapshots via Server.CreateImage", props.Type), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	props.Type = "snapshot"

	labels := mergeManagedLabels(props.Labels)

	description := props.Description
	opts := &hcloud.ServerCreateImageOpts{
		Type:        hcloud.ImageTypeSnapshot, // only snapshots are creatable via CreateImage
		Description: &description,
		Labels:      labels,
	}

	// CreateImage takes the SOURCE server and returns the new Image plus the
	// async Action that creates it. The new image's ID is the NativeID.
	res, _, err := c.Server().CreateImage(ctx, &hcloud.Server{ID: props.Server}, opts)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), mapHcloudError(err))}, nil
	}
	img := res.Image
	if img == nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", "create returned no image", resource.OperationErrorCodeInternalFailure)}, nil
	}
	action := res.Action
	if action == nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", "create returned no action", resource.OperationErrorCodeInternalFailure)}, nil
	}
	// Marshal observed state onto the InProgress result so the agent's
	// InProgress→Success transition preserves ResourceProperties (mirrors
	// server.go's create).
	b, _ := json.Marshal(imagePropertiesFrom(img))
	pr := progress(
		resource.OperationCreate, resource.OperationStatusInProgress,
		strconv.FormatInt(img.ID, 10), strconv.FormatInt(action.ID, 10),
	)
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
}

func (imageHandler) read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	img, _, err := c.Image().GetByID(ctx, id)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: mapHcloudError(err)}, nil
	}
	if img == nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeNotFound}, nil
	}
	props := imagePropertiesFrom(img)
	b, _ := json.Marshal(props)
	return &resource.ReadResult{ResourceType: req.ResourceType, Properties: string(b)}, nil
}

func (imageHandler) update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	var desired ImageProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("invalid image properties: %v", err), resource.OperationErrorCodeInvalidRequest)}, nil
	}
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}

	opts := hcloud.ImageUpdateOpts{}
	if desired.Description != "" {
		d := desired.Description
		opts.Description = &d
	}
	if desired.Labels != nil {
		opts.Labels = mergeManagedLabels(desired.Labels)
	}

	var img *hcloud.Image
	if opts.Description != nil || opts.Labels != nil {
		var err error
		img, _, err = c.Image().Update(ctx, &hcloud.Image{ID: id}, opts)
		if err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
		}
		if img == nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "image not found", resource.OperationErrorCodeNotFound)}, nil
		}
	} else {
		// Pure no-op: neither description nor labels present in desired
		// state. Skip the Update API call and read back the current image
		// so we can still return Success with observed properties (mirrors
		// floating_ip.go's guarded update).
		var err error
		img, _, err = c.Image().GetByID(ctx, id)
		if err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back failed: %v", err), mapHcloudError(err))}, nil
		}
		if img == nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "image not found", resource.OperationErrorCodeNotFound)}, nil
		}
	}

	b, _ := json.Marshal(imagePropertiesFrom(img))
	pr := progress(resource.OperationUpdate, resource.OperationStatusSuccess, req.NativeID, "")
	pr.ResourceProperties = b
	return &resource.UpdateResult{ProgressResult: pr}, nil
}

func (imageHandler) delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	id, err := parseNativeID(req.NativeID)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "invalid native id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	img, _, err := c.Image().GetByID(ctx, id)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	if img == nil {
		pr := progress(resource.OperationDelete, resource.OperationStatusFailure, req.NativeID, "")
		pr.ErrorCode = resource.OperationErrorCodeNotFound
		return &resource.DeleteResult{ProgressResult: pr}, nil
	}
	// hcloud-go's Image().Delete does not surface an Action (it uses
	// deleteRequestNoResult), so delete is treated as synchronous here.
	if _, err := c.Image().Delete(ctx, img); err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), mapHcloudError(err))}, nil
	}
	return &resource.DeleteResult{ProgressResult: progress(
		resource.OperationDelete, resource.OperationStatusSuccess,
		req.NativeID, "",
	)}, nil
}

func (imageHandler) list(ctx context.Context, c hcloudAPI, _ *resource.ListRequest) (*resource.ListResult, error) {
	images, err := c.Image().All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(images))
	for _, img := range images {
		// Only snapshots are user-manageable; system/app/backup images are
		// either read-only or created via a different path.
		if img.Type == hcloud.ImageTypeSnapshot {
			ids = append(ids, strconv.FormatInt(img.ID, 10))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func imagePropertiesFrom(img *hcloud.Image) ImageProperties {
	props := ImageProperties{
		Type:        string(img.Type),
		Description: img.Description,
		Labels:      stripManagedLabel(img.Labels),
		ID:          img.ID,
		Name:        img.Name,
		ImageSize:   float64(img.ImageSize),
		DiskSize:    float64(img.DiskSize),
		Status:      string(img.Status),
	}
	if img.CreatedFrom != nil {
		props.Server = img.CreatedFrom.ID
	}
	if !img.Created.IsZero() {
		props.Created = img.Created.Format(time.RFC3339)
	}
	return props
}
