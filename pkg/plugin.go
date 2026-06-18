// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: a formae resource plugin for Hetzner Cloud.
//
// This file holds the Plugin struct, the hcloud client adapter, the shared
// helpers, and the per-resource handler registry. Each resource type
// (Server, Network, Volume, ...) lives in its own file and self-registers
// via [register] in an init() — adding a new resource requires no edit to
// this file, so multi-resource work does not collide on a shared chokepoint.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// resourceHandler is the per-resource CRUD contract every resource file
// implements. A handler self-registers via [register] in its file's init().
//
// Status is intentionally NOT part of this interface: every async hcloud
// operation flows through an Action, so status polling is generic and lives
// at the Plugin level. Adding a new resource type is a matter of dropping in
// a new file that implements this interface and calling register.
type resourceHandler interface {
	create(ctx context.Context, c hcloudAPI, req *resource.CreateRequest) (*resource.CreateResult, error)
	read(ctx context.Context, c hcloudAPI, req *resource.ReadRequest) (*resource.ReadResult, error)
	update(ctx context.Context, c hcloudAPI, req *resource.UpdateRequest) (*resource.UpdateResult, error)
	delete(ctx context.Context, c hcloudAPI, req *resource.DeleteRequest) (*resource.DeleteResult, error)
	list(ctx context.Context, c hcloudAPI, req *resource.ListRequest) (*resource.ListResult, error)
}

// handlers is the package-level registry mapping formae resource types to
// their handlers. Populated by [register] calls in each resource file's
// init(); read by the dispatch methods on Plugin.
var handlers = map[string]resourceHandler{}

// register adds h as the handler for resourceType. It panics on a duplicate
// registration, since two handlers for one type is a programming error that
// would silently shadow one another. Each resource file calls register once
// from its init(), so the panic surfaces at program start.
func register(resourceType string, h resourceHandler) {
	if _, dup := handlers[resourceType]; dup {
		panic("formae-plugin-hcloud: duplicate handler for " + resourceType)
	}
	handlers[resourceType] = h
}

// hcloudAPI exposes the full set of hcloud-go sub-clients the plugin uses,
// using the generated per-resource interfaces from hcloud-go. *hcloud.Client
// exposes its sub-clients as struct fields rather than methods, so
// productionClient adapts it below.
//
// The interface is wider than the current Server-only implementation needs
// so later resource files (Network, Volume, Firewall, ...) can consume it
// without editing this file.
type hcloudAPI interface {
	Server() hcloud.IServerClient
	Network() hcloud.INetworkClient
	Volume() hcloud.IVolumeClient
	Firewall() hcloud.IFirewallClient
	LoadBalancer() hcloud.ILoadBalancerClient
	FloatingIP() hcloud.IFloatingIPClient
	PrimaryIP() hcloud.IPrimaryIPClient
	PlacementGroup() hcloud.IPlacementGroupClient
	Certificate() hcloud.ICertificateClient
	SSHKey() hcloud.ISSHKeyClient
	Image() hcloud.IImageClient
	Action() hcloud.IActionClient
}

// productionClient adapts a *hcloud.Client to the hcloudAPI interface by
// exposing its sub-client fields through method calls. Returning pointers
// to the embedded value-type clients matches the pointer-receiver method
// sets declared by hcloud-go.
type productionClient struct{ c *hcloud.Client }

func (p productionClient) Server() hcloud.IServerClient                 { return &p.c.Server }
func (p productionClient) Network() hcloud.INetworkClient               { return &p.c.Network }
func (p productionClient) Volume() hcloud.IVolumeClient                 { return &p.c.Volume }
func (p productionClient) Firewall() hcloud.IFirewallClient             { return &p.c.Firewall }
func (p productionClient) LoadBalancer() hcloud.ILoadBalancerClient     { return &p.c.LoadBalancer }
func (p productionClient) FloatingIP() hcloud.IFloatingIPClient         { return &p.c.FloatingIP }
func (p productionClient) PrimaryIP() hcloud.IPrimaryIPClient           { return &p.c.PrimaryIP }
func (p productionClient) PlacementGroup() hcloud.IPlacementGroupClient { return &p.c.PlacementGroup }
func (p productionClient) Certificate() hcloud.ICertificateClient       { return &p.c.Certificate }
func (p productionClient) SSHKey() hcloud.ISSHKeyClient                 { return &p.c.SSHKey }
func (p productionClient) Image() hcloud.IImageClient                   { return &p.c.Image }
func (p productionClient) Action() hcloud.IActionClient                 { return &p.c.Action }

// Compile-time assertion that the adapter satisfies hcloudAPI. If a future
// hcloud-go release renames a Client field or changes a method set, this
// fails at build time.
var _ hcloudAPI = productionClient{}

// Plugin implements the formae ResourcePlugin interface for Hetzner Cloud.
// A single Plugin handles every resource type in the HETZNER namespace,
// branching on req.ResourceType via the handlers registry.
type Plugin struct {
	mu sync.Mutex

	// client, when non-nil, short-circuits getClient. Tests inject a fake
	// here; production leaves it nil and a real hcloud.Client is built
	// lazily from the target config / HCLOUD_TOKEN env var.
	client hcloudAPI

	// cached holds the most recently built production client together with
	// the token it was built from. A client is only valid for one token, so
	// rather than caching a single client blindly we rebuild when the token
	// changes. This avoids reusing a client authenticated as the wrong token
	// if the target config rotates credentials.
	cached *cachedClient
}

// cachedClient pairs a token with the API built from it.
type cachedClient struct {
	token string
	api   hcloudAPI
}

var _ plugin.ResourcePlugin = &Plugin{}

// newPluginWithClient returns a Plugin wired to the supplied client,
// bypassing token resolution and the client cache. Intended for tests.
func newPluginWithClient(c hcloudAPI) *Plugin { return &Plugin{client: c} }

// resolveToken extracts the hcloud API token. A non-empty "token" field in
// the target config JSON ({"token":"..."}) wins; otherwise the HCLOUD_TOKEN
// environment variable is consulted. The legacy "Token" shape is accepted for
// compatibility with older schema output. Empty/nil/null target config falls
// back to the env var so existing deployments keep working.
//
// A malformed target config (genuine JSON parse error) is surfaced as an
// error rather than silently falling through to HCLOUD_TOKEN: a typo'd token
// config should not authenticate as a different credential drawn from the
// environment. An empty/null config ({"token":""}) still falls back, since
// that is the legacy "no config" shape.
func resolveToken(targetConfig json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(targetConfig))
	if len(trimmed) != 0 && trimmed != "null" {
		var cfg struct {
			Token       string `json:"token"`
			LegacyToken string `json:"Token"`
		}
		if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
			return "", fmt.Errorf("invalid hcloud target config JSON: %w", err)
		}
		if cfg.Token != "" {
			return cfg.Token, nil
		}
		if cfg.LegacyToken != "" {
			return cfg.LegacyToken, nil
		}
	}
	if token := os.Getenv("HCLOUD_TOKEN"); token != "" {
		return token, nil
	}
	return "", errors.New("hcloud API token not configured: set 'token' in the target config or the HCLOUD_TOKEN environment variable")
}

// getClient returns an hcloudAPI for the given target config, building one
// lazily from the resolved token and caching it keyed on that token.
func (p *Plugin) getClient(targetConfig json.RawMessage) (hcloudAPI, error) {
	// Injected (test) client bypasses the cache entirely.
	if p.client != nil {
		return p.client, nil
	}
	token, err := resolveToken(targetConfig)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && p.cached.token == token {
		return p.cached.api, nil
	}
	// Build the hcloud client with bounded HTTP timeout and reduced retries.
	//
	// hcloud-go's DEFAULTS are an unbounded HTTP client (&http.Client{}) and
	// 5 retries with exponential backoff (1+2+4+8+16s of backoff alone). If a
	// request hits a retryable error (rate-limit 429, transient 5xx, network
	// blip), the retries push the total RPC time well past the formae agent's
	// ~40s PluginOperator timeout — so the agent declares the operator
	// MissingInAction and fails the command, even though the plugin is still
	// sitting in a backoff sleep. This bit Server.Create (the heaviest call)
	// consistently.
	//
	// With a 10s per-request timeout and 1 retry (2 attempts total), the worst
	// case is ~10s + ~1s backoff + ~10s = ~21s — comfortably under the 40s
	// operator budget, leaving margin for serialization/RTT. Transient blips
	// are still covered by the single retry; a hard rate-limit surfaces as a
	// fast failure (which the agent reports cleanly) instead of a hang.
	//
	// The BackoffFunc MUST be set explicitly: WithRetryOpts preserves the
	// default backoff when it is nil, and hcloud-go's default is an exponential
	// backoff truncated to 60s. A single 60s backoff between two 10s attempts
	// is ~80s for ONE call — already double the operator budget — which is
	// what hung Server.Create (the heaviest call) past the budget and failed
	// the command. ConstantBackoff(1s) pins the worst case to the ~21s above.
	api := productionClient{c: hcloud.NewClient(
		hcloud.WithToken(token),
		hcloud.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
		hcloud.WithRetryOpts(hcloud.RetryOpts{
			MaxRetries:  1,
			BackoffFunc: hcloud.ConstantBackoff(1 * time.Second),
		}),
	)}
	p.cached = &cachedClient{token: token, api: api}
	return api, nil
}

// RateLimit returns a conservative hcloud API rate limit.
func (p *Plugin) RateLimit() model.RateLimitConfig {
	return model.RateLimitConfig{
		Scope:                            model.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 5,
	}
}

// DiscoveryFilters returns no filters (discover all servers).
func (p *Plugin) DiscoveryFilters() []model.MatchFilter { return nil }

// LabelConfig selects a human-readable attribute for discovered resources.
// Most hcloud resources expose a name, so $.name is the default. A Floating
// IP has no name field — its IP address is its unique identifier — so it
// gets a per-type override.
func (p *Plugin) LabelConfig() model.LabelConfig {
	return model.LabelConfig{
		DefaultQuery: "$.name",
		ResourceOverrides: map[string]string{
			FloatingIPResourceType: "$.ip",
		},
	}
}

// progress builds a ProgressResult with sensible defaults.
func progress(op resource.Operation, status resource.OperationStatus, nativeID, requestID string) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       op,
		OperationStatus: status,
		NativeID:        nativeID,
		RequestID:       requestID,
	}
}

func fail(op resource.Operation, nativeID, requestID, msg string, code resource.OperationErrorCode) *resource.ProgressResult {
	pr := progress(op, resource.OperationStatusFailure, nativeID, requestID)
	pr.ErrorCode = code
	pr.StatusMessage = msg
	return pr
}

// --- Shared helpers --------------------------------------------------------
//
// The helpers below standardise three things every per-resource handler
// needs: translating hcloud errors into formae error codes, asserting the
// managed_by=formae invariant on labels without leaking it into observed
// state, and parsing NativeIDs. Centralising them keeps every handler's
// parse/mapping path byte-identical and gives the test suite one place to
// lock down the contract.

// managedByLabelKey/Value is the synthetic label this plugin injects on every
// resource it manages. It is the plugin's own bookkeeping — never user data
// — so it is asserted on create/update and stripped from observed state.
const (
	managedByLabelKey   = "managed_by"
	managedByLabelValue = "formae"
)

// mapHcloudError translates an hcloud API error into the closest formae
// OperationErrorCode so the agent can react appropriately (back off on
// throttling, surface auth failures distinctly, etc.). The nil-error guard
// returns InternalFailure because a nil error reaching here is a plugin-local
// programming bug, not an upstream condition.
func mapHcloudError(err error) resource.OperationErrorCode {
	if err == nil {
		return resource.OperationErrorCodeInternalFailure
	}
	switch {
	case hcloud.IsError(err, hcloud.ErrorCodeUnauthorized):
		return resource.OperationErrorCodeInvalidCredentials
	case hcloud.IsError(err, hcloud.ErrorCodeForbidden):
		return resource.OperationErrorCodeAccessDenied
	case hcloud.IsError(err, hcloud.ErrorCodeTokenReadonly):
		return resource.OperationErrorCodeAccessDenied
	case hcloud.IsError(err, hcloud.ErrorCodeRateLimitExceeded):
		return resource.OperationErrorCodeThrottling
	case hcloud.IsError(err, hcloud.ErrorCodeNotFound):
		return resource.OperationErrorCodeNotFound
	case hcloud.IsError(err, hcloud.ErrorCodeInvalidInput):
		return resource.OperationErrorCodeInvalidRequest
	case hcloud.IsError(err, hcloud.ErrorCodeConflict), hcloud.IsError(err, hcloud.ErrorCodeLocked):
		return resource.OperationErrorCodeResourceConflict
	case hcloud.IsError(err, hcloud.ErrorCodeResourceLimitExceeded):
		return resource.OperationErrorCodeServiceLimitExceeded
	case hcloud.IsError(err, hcloud.ErrorCodeTimeout):
		return resource.OperationErrorCodeServiceTimeout
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Chain-level aggregate deadlines (e.g. the LB attach/reconcile bound)
		// surface as a service-side timeout so the agent backs off rather than
		// treating it as an internal plugin failure.
		return resource.OperationErrorCodeServiceTimeout
	default:
		// Unknown/unmapped hcloud errors and plain non-hcloud errors (e.g.
		// transient network blips surfaced as errors.New) are treated as
		// upstream service errors, not plugin bugs — matching the OVH
		// reference plugin's categorisation.
		return resource.OperationErrorCodeServiceInternalError
	}
}

// mergeManagedLabels returns a NEW map that is a copy of userLabels with the
// managed_by=formae invariant asserted. It NEVER mutates userLabels (the
// caller's desired-state map) — this standardises the previously inconsistent
// behaviour where create mutated the parsed map in place while update copied.
// A nil userLabels yields a map containing only the managed_by entry.
func mergeManagedLabels(userLabels map[string]string) map[string]string {
	out := make(map[string]string, len(userLabels)+1)
	for k, v := range userLabels {
		out[k] = v
	}
	out[managedByLabelKey] = managedByLabelValue
	return out
}

// stripManagedLabel returns a copy of labels with the synthetic managed_by
// entry removed, so observed state reported to formae does NOT contain the
// plugin's own bookkeeping label. Desired state (from PKL) never contains
// managed_by, so stripping it from observed keeps the two in sync and avoids
// a perpetual diff on reconcile. Returns nil if the resulting map is empty
// (so observed state carries no empty labels map).
func stripManagedLabel(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if k == managedByLabelKey {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseNativeID parses a formae NativeID (the string form of an hcloud int64
// resource ID) into an int64. Shared so every handler's parse+error path is
// byte-identical.
func parseNativeID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// --- Dispatch --------------------------------------------------------------
//
// Each CRUD method resolves the client, looks up the handler registered for
// req.ResourceType, and delegates. getClient failures and unknown types
// return the same failure shapes the resource files use internally so error
// handling is uniform across the plugin. Status stays at the Plugin level:
// every async hcloud op is an Action and shares one status enum.

// Create provisions a new resource, dispatching to the handler registered
// for req.ResourceType. Returns InProgress with the create Action's ID;
// formae polls Status until the resource is ready.
func (p *Plugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", err.Error(), resource.OperationErrorCodeInternalFailure)}, nil
	}
	h, ok := handlers[req.ResourceType]
	if !ok {
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, "", "", "unsupported resource type: "+req.ResourceType, resource.OperationErrorCodeInvalidRequest)}, nil
	}
	return h.create(ctx, client, req)
}

// Read returns the current state of a resource (NotFound if gone).
func (p *Plugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInternalFailure}, nil
	}
	h, ok := handlers[req.ResourceType]
	if !ok {
		return &resource.ReadResult{ResourceType: req.ResourceType, ErrorCode: resource.OperationErrorCodeInvalidRequest}, nil
	}
	return h.read(ctx, client, req)
}

// Update changes mutable fields of a resource. Most hcloud resource
// attributes are immutable and require recreation; handlers only forward
// the fields they know to be mutable.
func (p *Plugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", err.Error(), resource.OperationErrorCodeInternalFailure)}, nil
	}
	h, ok := handlers[req.ResourceType]
	if !ok {
		return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "unsupported resource type: "+req.ResourceType, resource.OperationErrorCodeInvalidRequest)}, nil
	}
	return h.update(ctx, client, req)
}

// Delete removes a resource, returning InProgress with the delete Action's
// ID. A NotFound result is treated as success by the formae agent.
func (p *Plugin) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", err.Error(), resource.OperationErrorCodeInternalFailure)}, nil
	}
	h, ok := handlers[req.ResourceType]
	if !ok {
		return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, req.NativeID, "", "unsupported resource type: "+req.ResourceType, resource.OperationErrorCodeInvalidRequest)}, nil
	}
	return h.delete(ctx, client, req)
}

// Status maps an hcloud Action's status to a formae OperationStatus. Status
// is generic — all async hcloud ops flow through Actions, so there is no
// per-handler status method.
//
// On Success, Status performs a best-effort read-back and attaches
// ResourceProperties when the resource still exists. This keeps async create
// and update flows from finishing with empty inventory properties. Read-back
// failures are not fatal: async delete success legitimately reads as NotFound,
// and some resources may have short eventual-consistency windows after the
// action completes.
func (p *Plugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, req.NativeID, req.RequestID, err.Error(), resource.OperationErrorCodeInternalFailure)}, nil
	}
	actionID, err := strconv.ParseInt(req.RequestID, 10, 64)
	if err != nil {
		return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, req.NativeID, req.RequestID, "invalid request id", resource.OperationErrorCodeInvalidRequest)}, nil
	}
	action, _, err := client.Action().GetByID(ctx, actionID)
	if err != nil {
		return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, req.NativeID, req.RequestID, err.Error(), mapHcloudError(err))}, nil
	}
	if action == nil {
		return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, req.NativeID, req.RequestID, "action not found", resource.OperationErrorCodeNotFound)}, nil
	}

	var status resource.OperationStatus
	switch action.Status {
	case hcloud.ActionStatusRunning:
		status = resource.OperationStatusInProgress
	case hcloud.ActionStatusSuccess:
		status = resource.OperationStatusSuccess
	case hcloud.ActionStatusError:
		return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, req.NativeID, req.RequestID, action.ErrorMessage, resource.OperationErrorCodeInternalFailure)}, nil
	default:
		status = resource.OperationStatusInProgress
	}
	pr := progress(resource.OperationCheckStatus, status, req.NativeID, req.RequestID)
	if status == resource.OperationStatusSuccess && req.ResourceType != "" && req.NativeID != "" {
		if h, ok := handlers[req.ResourceType]; ok {
			read, err := h.read(ctx, client, &resource.ReadRequest{
				NativeID:     req.NativeID,
				ResourceType: req.ResourceType,
				TargetConfig: req.TargetConfig,
			})
			if err == nil && read != nil && read.ErrorCode == "" && read.Properties != "" {
				pr.ResourceProperties = json.RawMessage(read.Properties)
			}
		}
	}
	return &resource.StatusResult{ProgressResult: pr}, nil
}

// List returns the native IDs of all resources of the given type (for
// discovery). Errors are surfaced, not hidden: an invalid token, a
// permission-denied, or a 5xx is returned to the caller so discovery /
// drift workflows can distinguish "no resources exist" from "the plugin
// could not enumerate resources". Returning an empty list on error would
// silently look like "no resources" — exactly the failure mode the formae
// agent uses to decide whether a resource is unmanaged, so masking the
// error would mask drift.
//
// The one exception is an unsupported resource type: the formae agent
// fans out List across every registered type, and a plugin that does not
// handle a given type legitimately has nothing to enumerate. That path
// stays an empty-list-with-log so the agent's fan-out can complete.
func (p *Plugin) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	client, err := p.getClient(req.TargetConfig)
	if err != nil {
		log.Printf("formae-plugin-hcloud: list discovery for %q failed: client resolution failed: %v", req.ResourceType, err)
		return nil, fmt.Errorf("list %q: %w", req.ResourceType, err)
	}
	h, ok := handlers[req.ResourceType]
	if !ok {
		// The formae agent fans out List across every registered resource
		// type; types this plugin does not handle legitimately enumerate
		// as empty. Surface this as an empty list (not an error) so the
		// fan-out completes, but log loudly so an unintended type is
		// visible.
		log.Printf("formae-plugin-hcloud: list discovery for %q skipped: unsupported resource type", req.ResourceType)
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}
	result, err := h.list(ctx, client, req)
	if err != nil {
		log.Printf("formae-plugin-hcloud: list discovery for %q failed: %v", req.ResourceType, err)
		return nil, fmt.Errorf("list %q: %w", req.ResourceType, err)
	}
	if result != nil {
		return result, nil
	}
	return &resource.ListResult{NativeIDs: []string{}}, nil
}
