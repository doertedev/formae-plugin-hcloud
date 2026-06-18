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

// --- Create ----------------------------------------------------------------
//
// These tests exercise serverHandler.create via Plugin.Create (the dispatch
// layer). ResourceType is set so the dispatch routes to the server handler;
// without it, the dispatch would short-circuit to InvalidRequest for
// "unsupported resource type" before the handler runs.

// TestCreate_InProgress_WithAction verifies that a normal async server create
// (Server + Action returned) reports InProgress with BOTH the server ID
// (nativeID, so the resource is addressable immediately) and the action ID
// (requestID, so the agent can poll Status). The handler must NOT do a
// GetByID before responding — that second call pushes the RPC past the formae
// agent's ~40s PluginOperator timeout (see the ⚠️ comment in server.go).
func TestCreate_InProgress_WithAction(t *testing.T) {
	var captured hcloud.ServerCreateOpts
	api := fakeAPI{server: fakeServerClient{
		create: func(_ context.Context, opts hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.ServerCreateResult{
				Server: &hcloud.Server{ID: 42, Name: "web-1"},
				Action: &hcloud.Action{ID: 7},
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ServerResourceType,
		Properties:   json.RawMessage(validProps),
		TargetConfig: json.RawMessage(`{"token":"x"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "42" {
		t.Errorf("NativeID: want 42, got %q", pr.NativeID)
	}
	if pr.RequestID != "7" {
		t.Errorf("RequestID: want 7 (action ID for Status polling), got %q", pr.RequestID)
	}
	// ResourceProperties come from the create-response Server (no GetByID).
	var props ServerProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid create-result properties: %v", err)
	}
	if props.Name != "web-1" {
		t.Errorf("create-result name: want web-1, got %q", props.Name)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if captured.Name != "web-1" {
		t.Errorf("expected name forwarded, got %q", captured.Name)
	}
}

// TestCreate_Success_WhenNoAction verifies the defensive fallback: if hcloud
// returns no provisioning Action (treats the create as synchronous, rare), the
// handler reports Success with the server ID.
func TestCreate_Success_WhenNoAction(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		create: func(_ context.Context, opts hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error) {
			return hcloud.ServerCreateResult{
				Server: &hcloud.Server{ID: 42, Name: "web-1"},
				// no Action
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ServerResourceType,
		Properties:   json.RawMessage(validProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status: want Success (no Action fallback), got %q", pr.OperationStatus)
	}
	if pr.NativeID != "42" {
		t.Errorf("NativeID: want 42, got %q", pr.NativeID)
	}
	if pr.RequestID != "" {
		t.Errorf("RequestID: want empty (no Action), got %q", pr.RequestID)
	}
}

func TestCreate_InvalidRequestMissingFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing-name", `{"serverType":"cx-example","image":"ubuntu"}`},
		{"missing-serverType", `{"name":"web","image":"ubuntu"}`},
		{"missing-image", `{"name":"web","serverType":"cx-example"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPluginWithClient(fakeAPI{})
			res, err := p.Create(context.Background(), &resource.CreateRequest{
				ResourceType: ServerResourceType,
				Properties:   json.RawMessage(c.json),
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
		})
	}
}

func TestCreate_APIError(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		create: func(context.Context, hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error) {
			return hcloud.ServerCreateResult{}, nil, errors.New("rate limited")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ServerResourceType,
		Properties:   json.RawMessage(validProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", code)
	}
	if msg := res.ProgressResult.StatusMessage; msg != "rate limited" {
		t.Errorf("StatusMessage: want %q, got %q", "rate limited", msg)
	}
}

// TestCreate_ForwardsAllFields verifies that SSH keys, userData, location,
// and user-supplied labels are forwarded into the hcloud.ServerCreateOpts the
// underlying client receives — alongside the injected managed_by=formae label.
func TestCreate_ForwardsAllFields(t *testing.T) {
	var captured hcloud.ServerCreateOpts
	api := fakeAPI{server: fakeServerClient{
		create: func(_ context.Context, opts hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error) {
			captured = opts
			return hcloud.ServerCreateResult{Server: &hcloud.Server{ID: 1}, Action: &hcloud.Action{ID: 1}}, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return sampleServer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	props := `{"name":"web-1","serverType":"cx-example","image":"ubuntu-22.04","location":"nbg1","sshKeys":["alpha","beta"],"userData":"#!/bin/sh","labels":{"team":"infra","tier":"web"}}`
	if _, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ServerResourceType,
		Properties:   json.RawMessage(props),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := len(captured.SSHKeys), 2; got != want {
		t.Fatalf("SSHKeys len: want %d, got %d", want, got)
	}
	gotNames := []string{captured.SSHKeys[0].Name, captured.SSHKeys[1].Name}
	if gotNames[0] != "alpha" || gotNames[1] != "beta" {
		t.Errorf("SSHKey names: want [alpha beta], got %v", gotNames)
	}
	if captured.UserData != "#!/bin/sh" {
		t.Errorf("UserData: want %q, got %q", "#!/bin/sh", captured.UserData)
	}
	if captured.Location == nil || captured.Location.Name != "nbg1" {
		t.Errorf("Location.Name: want nbg1, got %+v", captured.Location)
	}
	if got := captured.Labels["managed_by"]; got != "formae" {
		t.Errorf("expected managed_by=formae label injected, got %q", got)
	}
	if got := captured.Labels["team"]; got != "infra" {
		t.Errorf("expected user label team=infra preserved, got %q", got)
	}
	if got := captured.Labels["tier"]; got != "web" {
		t.Errorf("expected user label tier=web preserved, got %q", got)
	}
}

// --- Read ------------------------------------------------------------------

func TestRead_ExistingServer(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		getByID: func(_ context.Context, id int64) (*hcloud.Server, *hcloud.Response, error) {
			if id != 42 {
				t.Errorf("GetByID id: want 42, got %d", id)
			}
			return sampleServer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != "" {
		t.Errorf("ErrorCode: want empty, got %q", res.ErrorCode)
	}
	var props ServerProperties
	if err := json.Unmarshal([]byte(res.Properties), &props); err != nil {
		t.Fatalf("invalid properties JSON: %v", err)
	}
	if props.Name != "web-1" {
		t.Errorf("Name: want web-1, got %q", props.Name)
	}
	if props.ServerType != "cx-example" {
		t.Errorf("ServerType: want cx-example, got %q", props.ServerType)
	}
	if props.IPv4 != "203.0.113.10" {
		t.Errorf("IPv4: want 203.0.113.10, got %q", props.IPv4)
	}
}

func TestRead_NotFound(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", res.ErrorCode)
	}
}

func TestRead_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ServerResourceType,
		NativeID:     "not-a-number",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", res.ErrorCode)
	}
}

func TestRead_APIError(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, errors.New("boom")
		},
	}}
	p := newPluginWithClient(api)
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", res.ErrorCode)
	}
}

// --- Update ----------------------------------------------------------------

func TestUpdate_NameChangeSuccess(t *testing.T) {
	var updatedName string
	api := fakeAPI{server: fakeServerClient{
		update: func(_ context.Context, s *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			updatedName = opts.Name
			return s, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return sampleServer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-2","serverType":"cx-example","image":"ubuntu-22.04"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "42" {
		t.Errorf("NativeID: want 42, got %q", pr.NativeID)
	}
	if updatedName != "web-2" {
		t.Errorf("update not forwarded: want web-2, got %q", updatedName)
	}
	var props ServerProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid readback properties: %v", err)
	}
	if props.Name != "web-1" { // sampleServer() is named web-1
		t.Errorf("readback name: want web-1, got %q", props.Name)
	}
}

func TestUpdate_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "oops",
		DesiredProperties: json.RawMessage(validProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

func TestUpdate_UpdateAPIError(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		update: func(context.Context, *hcloud.Server, hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, errors.New("conflict")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-2","serverType":"cx-example","image":"ubuntu-22.04"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
	if pr.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", pr.ErrorCode)
	}
	if pr.StatusMessage != "conflict" {
		t.Errorf("StatusMessage: want %q, got %q", "conflict", pr.StatusMessage)
	}
}

func TestUpdate_ReadbackNotFound(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		update: func(_ context.Context, s *hcloud.Server, _ hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			return s, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-2","serverType":"cx-example","image":"ubuntu-22.04"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
	if pr.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", pr.ErrorCode)
	}
}

func TestUpdate_ReadbackError(t *testing.T) {
	// hcloud-go's GetByID returns a nil server alongside an error. The
	// read-back check distinguishes the two: an API error surfaces as
	// InternalFailure, while a genuine nil server (no error) surfaces as
	// NotFound (see TestUpdate_ReadbackNotFound).
	api := fakeAPI{server: fakeServerClient{
		update: func(_ context.Context, s *hcloud.Server, _ hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			return s, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, errors.New("readback boom")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-2","serverType":"cx-example","image":"ubuntu-22.04"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
	if pr.ErrorCode != resource.OperationErrorCodeServiceInternalError {
		t.Errorf("ErrorCode: want ServiceInternalError, got %q", pr.ErrorCode)
	}
	if pr.StatusMessage != "read-back failed: readback boom" {
		t.Errorf("StatusMessage: want %q, got %q", "read-back failed: readback boom", pr.StatusMessage)
	}
}

func TestUpdate_Labels(t *testing.T) {
	var captured hcloud.ServerUpdateOpts
	api := fakeAPI{server: fakeServerClient{
		update: func(_ context.Context, s *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			captured = opts
			return s, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return sampleServer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-1","serverType":"cx-example","image":"ubuntu-22.04","labels":{"team":"infra","tier":"web"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
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
	if got := captured.Labels["tier"]; got != "web" {
		t.Errorf("expected user label tier=web preserved, got %q", got)
	}
}

func TestUpdate_NameAndLabels(t *testing.T) {
	var captured hcloud.ServerUpdateOpts
	called := false
	api := fakeAPI{server: fakeServerClient{
		update: func(_ context.Context, s *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
			captured = opts
			called = true
			return s, nil, nil
		},
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return sampleServer(), nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(`{"name":"web-2","serverType":"cx-example","image":"ubuntu-22.04","labels":{"team":"infra"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if !called {
		t.Fatal("expected a single Update call covering both name and labels")
	}
	if captured.Name != "web-2" {
		t.Errorf("Name: want web-2, got %q", captured.Name)
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

// --- Delete ----------------------------------------------------------------

func TestDelete_Success(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return sampleServer(), nil, nil
		},
		deleteWithResult: func(_ context.Context, s *hcloud.Server) (*hcloud.ServerDeleteResult, *hcloud.Response, error) {
			if s.ID != 42 {
				t.Errorf("delete id: want 42, got %d", s.ID)
			}
			return &hcloud.ServerDeleteResult{Action: &hcloud.Action{ID: 99}}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", pr.OperationStatus)
	}
	if pr.NativeID != "42" {
		t.Errorf("NativeID: want 42, got %q", pr.NativeID)
	}
	if pr.RequestID != "99" {
		t.Errorf("RequestID: want 99, got %q", pr.RequestID)
	}
}

func TestDelete_AlreadyGone(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
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

func TestDelete_InvalidNativeID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ServerResourceType,
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

func TestList_Success(t *testing.T) {
	api := fakeAPI{server: fakeServerClient{
		all: func(context.Context) ([]*hcloud.Server, error) {
			return []*hcloud.Server{
				{ID: 1},
				{ID: 2},
				{ID: 3},
			}, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: ServerResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"1", "2", "3"}
	if len(res.NativeIDs) != len(want) {
		t.Fatalf("NativeIDs: want %v, got %v", want, res.NativeIDs)
	}
	for i, id := range want {
		if res.NativeIDs[i] != id {
			t.Errorf("NativeIDs[%d]: want %q, got %q", i, id, res.NativeIDs[i])
		}
	}
}

func TestList_PropagatesAPIError(t *testing.T) {
	// Discovery errors must be surfaced, not hidden as empty lists —
	// otherwise an invalid token or a 5xx during discovery looks like "no
	// resources" to the drift workflow.
	api := fakeAPI{server: fakeServerClient{
		all: func(context.Context) ([]*hcloud.Server, error) {
			return nil, errors.New("unavailable")
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: ServerResourceType})
	if err == nil {
		t.Fatalf("expected error from List on API failure, got nil (result=%+v)", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}

// --- Observed-state mapper --------------------------------------------------
//
// TestServerPropertiesFrom_StripsManagedLabel locks down Finding #4: the
// plugin's synthetic managed_by=formae label is injected on every write path
// (create/update) but MUST NOT leak into observed state — desired state (PKL)
// never carries it, so leaving it in observed state produces a perpetual
// diff on every reconcile. Other user labels must survive.
func TestServerPropertiesFrom_StripsManagedLabel(t *testing.T) {
	s := &hcloud.Server{
		ID:     42,
		Name:   "web-1",
		Labels: map[string]string{"managed_by": "formae", "owner": "x"},
	}
	props := serverPropertiesFrom(s)
	if _, ok := props.Labels["managed_by"]; ok {
		t.Errorf("expected managed_by stripped from observed state, got %q", props.Labels["managed_by"])
	}
	if got := props.Labels["owner"]; got != "x" {
		t.Errorf("expected user label owner=x preserved, got %q", got)
	}
}
