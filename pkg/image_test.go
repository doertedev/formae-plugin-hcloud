// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// fakeImageClient implements hcloud.IImageClient. The four methods the suite
// exercises dispatch to function fields (nil field => zero response); the
// remaining methods are inherited from the nil-embedded interface.
type fakeImageClient struct {
	hcloud.IImageClient

	getByID func(context.Context, int64) (*hcloud.Image, *hcloud.Response, error)
	update  func(context.Context, *hcloud.Image, hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error)
	delete  func(context.Context, *hcloud.Image) (*hcloud.Response, error)
	all     func(context.Context) ([]*hcloud.Image, error)
}

func (f fakeImageClient) GetByID(ctx context.Context, id int64) (*hcloud.Image, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeImageClient) Update(ctx context.Context, img *hcloud.Image, opts hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, img, opts)
}

func (f fakeImageClient) Delete(ctx context.Context, img *hcloud.Image) (*hcloud.Response, error) {
	if f.delete == nil {
		return nil, nil
	}
	return f.delete(ctx, img)
}

func (f fakeImageClient) All(ctx context.Context) ([]*hcloud.Image, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

var _ hcloud.IImageClient = fakeImageClient{}

// fakeServerCreateImage provides the CreateImage method of IServerClient by
// populating the embedded interface on fakeServerClient. fakeServerClient
// defines explicit methods for Create/GetByID/Update/Delete/All (which shadow
// the embedded interface), but does NOT define CreateImage — so CreateImage
// is promoted from the embedded IServerClient. Setting that embedded field to
// a fakeServerCreateImage value routes CreateImage calls to its createImage
// function field without requiring an edit to plugin_test.go.
type fakeServerCreateImage struct {
	hcloud.IServerClient

	createImage func(context.Context, *hcloud.Server, *hcloud.ServerCreateImageOpts) (hcloud.ServerCreateImageResult, *hcloud.Response, error)
}

func (f fakeServerCreateImage) CreateImage(ctx context.Context, s *hcloud.Server, opts *hcloud.ServerCreateImageOpts) (hcloud.ServerCreateImageResult, *hcloud.Response, error) {
	if f.createImage == nil {
		return hcloud.ServerCreateImageResult{}, nil, nil
	}
	return f.createImage(ctx, s, opts)
}

const validImageProps = `{"server":42}`

func sampleImage() *hcloud.Image {
	return &hcloud.Image{
		ID:          99,
		Name:        "snap-1",
		Type:        hcloud.ImageTypeSnapshot,
		Status:      hcloud.ImageStatusAvailable,
		Description: "a snapshot",
		ImageSize:   5.0,
		DiskSize:    10.0,
		Labels:      map[string]string{"managed_by": "formae"},
		CreatedFrom: &hcloud.Server{ID: 42},
	}
}

// --- Create ----------------------------------------------------------------

func TestImage_Create_Success(t *testing.T) {
	var capturedServerID int64
	var capturedOpts *hcloud.ServerCreateImageOpts
	api := fakeAPI{
		server: fakeServerClient{
			IServerClient: fakeServerCreateImage{
				createImage: func(_ context.Context, s *hcloud.Server, opts *hcloud.ServerCreateImageOpts) (hcloud.ServerCreateImageResult, *hcloud.Response, error) {
					capturedServerID = s.ID
					capturedOpts = opts
					return hcloud.ServerCreateImageResult{
						Image:  sampleImage(),
						Action: &hcloud.Action{ID: 55},
					}, nil, nil
				},
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ImageResourceType,
		Properties:   json.RawMessage(validImageProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("status: want InProgress, got %q", pr.OperationStatus)
	}
	// NativeID is the IMAGE id, not the server id.
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99 (image id), got %q", pr.NativeID)
	}
	if pr.RequestID != "55" {
		t.Errorf("RequestID: want 55, got %q", pr.RequestID)
	}
	if capturedServerID != 42 {
		t.Errorf("source server id: want 42, got %d", capturedServerID)
	}
	if capturedOpts.Type != hcloud.ImageTypeSnapshot {
		t.Errorf("opts Type: want snapshot, got %q", capturedOpts.Type)
	}
	if got := capturedOpts.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if capturedOpts.Description == nil || *capturedOpts.Description != "" {
		t.Errorf("expected Description pointer forwarded, got %+v", capturedOpts.Description)
	}
	// F2: ResourceProperties must be populated on the InProgress result so
	// the agent's InProgress→Success transition preserves observed state
	// (mirrors server.go's create).
	if len(pr.ResourceProperties) == 0 {
		t.Fatal("expected ResourceProperties to be populated on InProgress")
	}
	var observed ImageProperties
	if err := json.Unmarshal(pr.ResourceProperties, &observed); err != nil {
		t.Fatalf("invalid ResourceProperties JSON: %v", err)
	}
	if observed.ID != 99 {
		t.Errorf("ResourceProperties ID: want 99, got %d", observed.ID)
	}
}

func TestImage_Create_MissingServer(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ImageResourceType,
		Properties:   json.RawMessage(`{"description":"no server"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", res.ProgressResult.OperationStatus)
	}
}

func TestImage_Create_APIError(t *testing.T) {
	api := fakeAPI{
		server: fakeServerClient{
			IServerClient: fakeServerCreateImage{
				createImage: func(context.Context, *hcloud.Server, *hcloud.ServerCreateImageOpts) (hcloud.ServerCreateImageResult, *hcloud.Response, error) {
					return hcloud.ServerCreateImageResult{}, nil, errors.New("server locked")
				},
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ImageResourceType,
		Properties:   json.RawMessage(validImageProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
	if msg := res.ProgressResult.StatusMessage; msg != "server locked" {
		t.Errorf("StatusMessage: want %q, got %q", "server locked", msg)
	}
}

func TestImage_Create_RejectsUnsupportedType(t *testing.T) {
	// Guard: CreateImage must NOT be called — type validation rejects the
	// request before it reaches the SDK.
	var called bool
	api := fakeAPI{
		server: fakeServerClient{
			IServerClient: fakeServerCreateImage{
				createImage: func(context.Context, *hcloud.Server, *hcloud.ServerCreateImageOpts) (hcloud.ServerCreateImageResult, *hcloud.Response, error) {
					called = true
					return hcloud.ServerCreateImageResult{}, nil, nil
				},
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ImageResourceType,
		Properties:   json.RawMessage(`{"server":42,"type":"backup"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
	if code := pr.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
	if called {
		t.Fatal("CreateImage should not be called for an unsupported type")
	}
}

// --- Read ------------------------------------------------------------------

func TestImage_Read_Existing(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Image, *hcloud.Response, error) {
			if id != 99 {
				t.Errorf("GetByID id: want 99, got %d", id)
			}
			return sampleImage(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ImageResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props ImageProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.ID != 99 {
		t.Errorf("ID: want 99, got %d", props.ID)
	}
	if props.Type != "snapshot" {
		t.Errorf("Type: want snapshot, got %q", props.Type)
	}
	if props.Server != 42 {
		t.Errorf("Server (from CreatedFrom): want 42, got %d", props.Server)
	}
}

func TestImage_Read_NotFound(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		getByID: func(context.Context, int64) (*hcloud.Image, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ImageResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestImage_Read_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ImageResourceType,
		NativeID:     "nope",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestImage_Update_Labels(t *testing.T) {
	var captured hcloud.ImageUpdateOpts
	api := fakeAPI{image: fakeImageClient{
		update: func(_ context.Context, img *hcloud.Image, opts hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error) {
			captured = opts
			return sampleImage(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ImageResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"server":42,"description":"a snapshot","type":"snapshot","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if captured.Labels == nil {
		t.Fatal("expected Labels to be set in update opts")
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if got := captured.Labels["team"]; got != "infra" {
		t.Errorf("expected user label team=infra preserved, got %q", got)
	}
}

func TestImage_Update_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ImageResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validImageProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

func TestImage_Update_Description(t *testing.T) {
	var captured hcloud.ImageUpdateOpts
	api := fakeAPI{image: fakeImageClient{
		update: func(_ context.Context, img *hcloud.Image, opts hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error) {
			if img.ID != 99 {
				t.Errorf("update image id: want 99, got %d", img.ID)
			}
			captured = opts
			return sampleImage(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	// No "server" field — before the Bug 2 fix this would have failed
	// parseImageProperties with "image properties missing 'server'".
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ImageResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"description":"new desc"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if captured.Description == nil {
		t.Fatal("expected Description to be forwarded, got nil")
	}
	if got := *captured.Description; got != "new desc" {
		t.Errorf("Description: want %q, got %q", "new desc", got)
	}
}

func TestImage_Update_NoDescription_NoChange(t *testing.T) {
	var captured hcloud.ImageUpdateOpts
	api := fakeAPI{image: fakeImageClient{
		update: func(_ context.Context, img *hcloud.Image, opts hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error) {
			captured = opts
			return sampleImage(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	// No "description" field at all — before the Bug 1 fix the handler
	// forwarded a pointer to "" and cleared the existing description.
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ImageResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if captured.Description != nil {
		t.Errorf("expected Description NOT to be sent (nil), got %q", *captured.Description)
	}
	if captured.Labels == nil {
		t.Fatal("expected Labels to be set in update opts")
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
}

func TestImage_Update_NoOp_SkipsAPICall(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		update: func(context.Context, *hcloud.Image, hcloud.ImageUpdateOpts) (*hcloud.Image, *hcloud.Response, error) {
			t.Fatal("Update should not be called for a no-op")
			return nil, nil, nil
		},
		getByID: func(_ context.Context, id int64) (*hcloud.Image, *hcloud.Response, error) {
			if id != 99 {
				t.Errorf("GetByID id: want 99, got %d", id)
			}
			return sampleImage(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	// Neither description nor labels present: a pure no-op. The handler must
	// skip Update and fall back to a read-back via GetByID.
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ImageResourceType,
		NativeID:          "99",
		DesiredProperties: json.RawMessage(`{"type":"snapshot"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if len(pr.ResourceProperties) == 0 {
		t.Fatal("expected ResourceProperties to be populated")
	}
	var observed ImageProperties
	if err := json.Unmarshal(pr.ResourceProperties, &observed); err != nil {
		t.Fatalf("invalid ResourceProperties JSON: %v", err)
	}
	if observed.ID != 99 {
		t.Errorf("ResourceProperties ID: want 99, got %d", observed.ID)
	}
}

// --- Delete ----------------------------------------------------------------

func TestImage_Delete_Success(t *testing.T) {
	api := fakeAPI{
		image: fakeImageClient{
			getByID: func(context.Context, int64) (*hcloud.Image, *hcloud.Response, error) {
				return sampleImage(), nil, nil
			},
			delete: func(_ context.Context, img *hcloud.Image) (*hcloud.Response, error) {
				if img.ID != 99 {
					t.Errorf("delete id: want 99, got %d", img.ID)
				}
				return nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ImageResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	// hcloud-go's Image().Delete does not surface an Action — synchronous.
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "99" {
		t.Errorf("NativeID: want 99, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (sync), got %q", pr.RequestID)
	}
}

func TestImage_Delete_AlreadyGone(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		getByID: func(context.Context, int64) (*hcloud.Image, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ImageResourceType,
		NativeID:     "99",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", pr.ErrorCode)
	}
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
}

func TestImage_Delete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ImageResourceType,
		NativeID:     "nope",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

// --- List ------------------------------------------------------------------

func TestImage_List_FiltersSnapshots(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		all: func(context.Context) ([]*hcloud.Image, error) {
			return []*hcloud.Image{
				{ID: 1, Type: hcloud.ImageTypeSystem},
				{ID: 2, Type: hcloud.ImageTypeSnapshot},
				{ID: 3, Type: hcloud.ImageTypeApp},
				{ID: 4, Type: hcloud.ImageTypeSnapshot},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: ImageResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only snapshot images (IDs 2 and 4) should be listed.
	want := []string{"2", "4"}
	if len(res.NativeIDs) != len(want) {
		t.Fatalf("NativeIDs: want %v, got %v", want, res.NativeIDs)
	}
	for i, id := range want {
		if res.NativeIDs[i] != id {
			t.Errorf("NativeIDs[%d]: want %q, got %q", i, id, res.NativeIDs[i])
		}
	}
}

func TestImage_List_EmptyOnError(t *testing.T) {
	api := fakeAPI{image: fakeImageClient{
		all: func(context.Context) ([]*hcloud.Image, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: ImageResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list on API error, got %v", res.NativeIDs)
	}
}
