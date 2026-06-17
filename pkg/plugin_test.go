// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// --- Fake hcloud client ----------------------------------------------------
//
// The fake hcloudAPI must satisfy the full, wider interface defined in
// plugin.go even though the Server-only test suite only exercises the
// Server and Action sub-clients. The unused sub-clients are stubbed as
// zero-struct types that embed their generated interface: that promotes
// every interface method onto the struct so it satisfies the interface at
// compile time, and a call to any of them panics with a nil-dereference
// (clearly flagging an unintended call) since the embedded interface is
// nil. fakeServerClient/fakeActionClient use the same embedding trick to
// inherit the methods they don't override, while defining the handful they
// do override as function-field dispatchers (so each test only wires up
// the methods it exercises).

// fakeServerClient implements hcloud.IServerClient. The five methods the
// suite exercises dispatch to function fields (nil field => zero response);
// the remaining methods are inherited from the nil-embedded interface.
type fakeServerClient struct {
	hcloud.IServerClient

	create           func(context.Context, hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error)
	getByID          func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error)
	update           func(context.Context, *hcloud.Server, hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error)
	deleteWithResult func(context.Context, *hcloud.Server) (*hcloud.ServerDeleteResult, *hcloud.Response, error)
	all              func(context.Context) ([]*hcloud.Server, error)
}

func (f fakeServerClient) Create(ctx context.Context, opts hcloud.ServerCreateOpts) (hcloud.ServerCreateResult, *hcloud.Response, error) {
	if f.create == nil {
		return hcloud.ServerCreateResult{}, nil, nil
	}
	return f.create(ctx, opts)
}

func (f fakeServerClient) GetByID(ctx context.Context, id int64) (*hcloud.Server, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

func (f fakeServerClient) Update(ctx context.Context, s *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
	if f.update == nil {
		return nil, nil, nil
	}
	return f.update(ctx, s, opts)
}

func (f fakeServerClient) DeleteWithResult(ctx context.Context, s *hcloud.Server) (*hcloud.ServerDeleteResult, *hcloud.Response, error) {
	if f.deleteWithResult == nil {
		return nil, nil, nil
	}
	return f.deleteWithResult(ctx, s)
}

func (f fakeServerClient) All(ctx context.Context) ([]*hcloud.Server, error) {
	if f.all == nil {
		return nil, nil
	}
	return f.all(ctx)
}

// fakeActionClient implements hcloud.IActionClient via the same nil-embedded
// interface trick; only GetByID is overridden.
type fakeActionClient struct {
	hcloud.IActionClient

	getByID func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error)
}

func (f fakeActionClient) GetByID(ctx context.Context, id int64) (*hcloud.Action, *hcloud.Response, error) {
	if f.getByID == nil {
		return nil, nil, nil
	}
	return f.getByID(ctx, id)
}

// The remaining sub-clients are not exercised by the suite. Each type below
// embeds its generated interface (nil) to satisfy hcloudAPI at compile time;
// calling any method on one panics with a nil-dereference, surfacing the
// unintended call loudly. The non-Server/Action sub-clients are held as
// interface fields so each resource's test file defines its own fake (in its
// own file) and assigns it here — no shared-file conflicts when adding resources.

// fakeAPI is a hand-written hcloudAPI for tests. Server and Action carry
// concrete fakes (fakeServerClient/fakeActionClient, used by the dispatch and
// Status tests). The remaining sub-clients are interface fields left nil by
// default; a resource test sets the ones it exercises (e.g. `api.network = ...`).
type fakeAPI struct {
	server fakeServerClient
	action fakeActionClient

	network        hcloud.INetworkClient
	volume         hcloud.IVolumeClient
	firewall       hcloud.IFirewallClient
	loadBalancer   hcloud.ILoadBalancerClient
	floatingIP     hcloud.IFloatingIPClient
	primaryIP      hcloud.IPrimaryIPClient
	placementGroup hcloud.IPlacementGroupClient
	certificate    hcloud.ICertificateClient
	sshKey         hcloud.ISSHKeyClient
	image          hcloud.IImageClient
}

func (f fakeAPI) Server() hcloud.IServerClient                 { return f.server }
func (f fakeAPI) Network() hcloud.INetworkClient               { return f.network }
func (f fakeAPI) Volume() hcloud.IVolumeClient                 { return f.volume }
func (f fakeAPI) Firewall() hcloud.IFirewallClient             { return f.firewall }
func (f fakeAPI) LoadBalancer() hcloud.ILoadBalancerClient     { return f.loadBalancer }
func (f fakeAPI) FloatingIP() hcloud.IFloatingIPClient         { return f.floatingIP }
func (f fakeAPI) PrimaryIP() hcloud.IPrimaryIPClient           { return f.primaryIP }
func (f fakeAPI) PlacementGroup() hcloud.IPlacementGroupClient { return f.placementGroup }
func (f fakeAPI) Certificate() hcloud.ICertificateClient       { return f.certificate }
func (f fakeAPI) SSHKey() hcloud.ISSHKeyClient                 { return f.sshKey }
func (f fakeAPI) Image() hcloud.IImageClient                   { return f.image }
func (f fakeAPI) Action() hcloud.IActionClient                 { return f.action }

// Compile-time checks that fakes satisfy the interfaces.
var (
	_ hcloud.IServerClient = fakeServerClient{}
	_ hcloud.IActionClient = fakeActionClient{}
	_ hcloudAPI            = fakeAPI{}
)

// validProps is a minimal ServerProperties JSON blob that parses cleanly.
const validProps = `{"name":"web-1","serverType":"cx-example","image":"ubuntu-24.04","location":"nbg1"}`

// sampleServer returns a populated hcloud.Server for read-back assertions.
func sampleServer() *hcloud.Server {
	return &hcloud.Server{
		ID:         42,
		Name:       "web-1",
		ServerType: &hcloud.ServerType{Name: "cx-example"},
		Image:      &hcloud.Image{Name: "ubuntu-22.04"},
		Location:   &hcloud.Location{Name: "nbg1"},
		Labels:     map[string]string{"managed_by": "formae"},
		PublicNet: hcloud.ServerPublicNet{
			IPv4: hcloud.ServerPublicNetIPv4{IP: net.ParseIP("203.0.113.10")},
		},
	}
}

// --- resolveToken / getClient -----------------------------------------------

func TestResolveToken_ConfigWins(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	tok, err := resolveToken([]byte(`{"token":"cfgtok"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "cfgtok" {
		t.Fatalf("expected cfgtok, got %q", tok)
	}
}

func TestResolveToken_LegacyFormaeConfigShapeWins(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	tok, err := resolveToken([]byte(`{"Type":"HETZNER","Token":"cfgtok"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "cfgtok" {
		t.Fatalf("expected cfgtok, got %q", tok)
	}
}

func TestResolveToken_FallsBackToEnv(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	cases := []struct {
		name string
		cfg  string
	}{
		{"empty", ""},
		{"nil-json", "null"},
		{"no-token-field", `{}`},
		{"empty-token", `{"token":""}`},
		{"whitespace", "  \t\n "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, err := resolveToken(json.RawMessage(c.cfg))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tok != "envtok" {
				t.Fatalf("expected envtok, got %q", tok)
			}
		})
	}
}

func TestResolveToken_ErrorsWhenMissing(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	_, err := resolveToken(nil)
	if err == nil {
		t.Fatal("expected error when no token is configured")
	}
}

func TestGetClient_CachesByToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}

	c1, err := p.getClient([]byte(`{"token":"abc"}`))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	c2, err := p.getClient([]byte(`{"token":"abc"}`))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c1 != c2 {
		t.Fatal("expected cached client to be reused for the same token")
	}

	c3, err := p.getClient([]byte(`{"token":"xyz"}`))
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if c3 == c1 {
		t.Fatal("expected a new client when the token changes")
	}
}

func TestGetClient_InjectedClientShortCircuits(t *testing.T) {
	// Use a pointer so the interface value is comparable — fakeAPI contains
	// function fields, so a value-typed fakeAPI is not.
	api := &fakeAPI{}
	p := newPluginWithClient(api)

	got, err := p.getClient(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != hcloudAPI(api) {
		t.Fatalf("expected injected client to be returned as-is, got %T", got)
	}
}

// --- Dispatch: unknown resource types --------------------------------------

// TestCreate_UnknownResourceType verifies the dispatch returns InvalidRequest
// (not a panic, not InternalFailure) when no handler is registered for the
// requested type. This is the contract every future resource type relies on
// for graceful failure when mistyped.
func TestCreate_UnknownResourceType(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "HETZNER::Compute::Mystery",
		Properties:   json.RawMessage(validProps),
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

// TestList_UnknownResourceType verifies an unknown type yields an empty
// NativeIDs slice (not an error), matching the existing List contract.
func TestList_UnknownResourceType(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.List(context.Background(), &resource.ListRequest{
		ResourceType: "HETZNER::Compute::Mystery",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.NativeIDs == nil {
		t.Fatal("expected non-nil NativeIDs slice")
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty NativeIDs, got %v", res.NativeIDs)
	}
}

// --- No-token paths (Plugin-level, before any handler is reached) ----------

func TestCreate_NoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ServerResourceType,
		Properties:   json.RawMessage(validProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", code)
	}
}

func TestRead_NoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}
	res, err := p.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrorCode != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", res.ErrorCode)
	}
}

func TestUpdate_NoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}
	res, err := p.Update(context.Background(), &resource.UpdateRequest{
		ResourceType:      ServerResourceType,
		NativeID:          "42",
		DesiredProperties: json.RawMessage(validProps),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", code)
	}
}

func TestDelete_NoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}
	res, err := p.Delete(context.Background(), &resource.DeleteRequest{
		ResourceType: ServerResourceType,
		NativeID:     "42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", code)
	}
}

func TestList_EmptyOnNoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}

	res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: ServerResourceType})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("expected empty list when no token is configured, got %v", res.NativeIDs)
	}
}

// --- Status (Plugin-level — shared across all resource types) --------------

func TestStatus_RunningAction(t *testing.T) {
	api := fakeAPI{action: fakeActionClient{
		getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
			return &hcloud.Action{ID: 7, Status: hcloud.ActionStatusRunning}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := res.ProgressResult.OperationStatus; s != resource.OperationStatusInProgress {
		t.Errorf("Status: want InProgress, got %q", s)
	}
}

func TestStatus_SuccessAction(t *testing.T) {
	api := fakeAPI{action: fakeActionClient{
		getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
			return &hcloud.Action{ID: 7, Status: hcloud.ActionStatusSuccess}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := res.ProgressResult.OperationStatus; s != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", s)
	}
}

func TestStatus_SuccessAction_AttachesReadBackProperties(t *testing.T) {
	api := fakeAPI{
		action: fakeActionClient{
			getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: 7, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		server: fakeServerClient{
			getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
				return sampleServer(), nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{
		NativeID:     "42",
		RequestID:    "7",
		ResourceType: ServerResourceType,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if len(pr.ResourceProperties) == 0 {
		t.Fatal("expected ResourceProperties from read-back")
	}
	var props ServerProperties
	if err := json.Unmarshal(pr.ResourceProperties, &props); err != nil {
		t.Fatalf("invalid ResourceProperties JSON: %v", err)
	}
	if props.ID != 42 {
		t.Errorf("ResourceProperties ID: want 42, got %d", props.ID)
	}
}

func TestStatus_SuccessAction_ReadBackNotFoundStillSucceeds(t *testing.T) {
	api := fakeAPI{
		action: fakeActionClient{
			getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
				return &hcloud.Action{ID: 7, Status: hcloud.ActionStatusSuccess}, nil, nil
			},
		},
		server: fakeServerClient{
			getByID: func(context.Context, int64) (*hcloud.Server, *hcloud.Response, error) {
				return nil, nil, nil
			},
		},
	}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{
		NativeID:     "42",
		RequestID:    "7",
		ResourceType: ServerResourceType,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("Status: want Success, got %q", pr.OperationStatus)
	}
	if len(pr.ResourceProperties) != 0 {
		t.Errorf("expected empty ResourceProperties on NotFound read-back, got %q", string(pr.ResourceProperties))
	}
}

func TestStatus_ErrorAction(t *testing.T) {
	api := fakeAPI{action: fakeActionClient{
		getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
			return &hcloud.Action{
				ID:           7,
				Status:       hcloud.ActionStatusError,
				ErrorMessage: "action failed",
			}, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("Status: want Failure, got %q", pr.OperationStatus)
	}
	if pr.ErrorCode != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", pr.ErrorCode)
	}
	if pr.StatusMessage != "action failed" {
		t.Errorf("StatusMessage: want %q, got %q", "action failed", pr.StatusMessage)
	}
}

func TestStatus_NotFoundAction(t *testing.T) {
	api := fakeAPI{action: fakeActionClient{
		getByID: func(context.Context, int64) (*hcloud.Action, *hcloud.Response, error) {
			return nil, nil, nil
		},
	}}
	p := newPluginWithClient(api)

	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode: want NotFound, got %q", code)
	}
}

func TestStatus_InvalidRequestID(t *testing.T) {
	p := newPluginWithClient(fakeAPI{})
	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "bad"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode: want InvalidRequest, got %q", code)
	}
}

func TestStatus_NoToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	p := &Plugin{}
	res, err := p.Status(context.Background(), &resource.StatusRequest{NativeID: "42", RequestID: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code := res.ProgressResult.ErrorCode; code != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ErrorCode: want InternalFailure, got %q", code)
	}
}
