// SPDX-License-Identifier: Apache-2.0
//
// formae-plugin-hcloud: HETZNER::Network::LoadBalancer handler.
//
// Self-registers via init() — no shared file needs to change to add this
// resource. Mirrors the layout of server.go: properties struct, parser,
// observed-state mapper, handler, init/register.
//
// Signature notes (verified in hcloud-go v2.43.0):
//   - ILoadBalancerClient.Create returns (LoadBalancerCreateResult, *Response, error)
//     with a single create Action. The LB itself is not addressable until the
//     create Action succeeds, so services/targets cannot be added inline.
//   - AddService / UpdateService / DeleteService / AddServerTarget /
//     AddLabelSelectorTarget / AddIPTarget / Remove*Target each return a
//     single *Action — all are async.
//   - Update only carries name and labels. Algorithm/Type/Location/Zone are
//     immutable post-create (the LB does have ChangeAlgorithm/ChangeType RPCs
//     but those are intentionally out of scope here).
//
// Create-time flow with services/targets:
//   1. Issue Create (async).
//   2. Wait for the create Action to settle synchronously — services/targets
//      can only be added once the LB is up.
//   3. Add each service and target in declaration order, waiting for each
//      Action inline. Failing to wait would race the LB's per-LB concurrency
//      limit and surface as hcloud `conflict` errors.
//   4. Read back the full LB and return Success with the complete properties.
//
// When no services/targets are declared, the handler preserves the legacy
// fast path: return InProgress with the create Action's ID and let the
// framework's Status→Success transition drive the read-back. This keeps the
// no-services create as cheap as it was before services/targets were added.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// LoadBalancerResourceType is the formae resource type for a Hetzner Cloud
// Load Balancer.
const LoadBalancerResourceType = "HETZNER::Network::LoadBalancer"

// lbActionPollInterval is the cadence at which the create-flow action waiter
// re-queries an hcloud Action. hcloud Actions for LBs typically settle in a
// couple of seconds, so a sub-second poll keeps the synchronous create path
// snappy without hammering the API.
const lbActionPollInterval = 1 * time.Second

// lbActionTimeout bounds the synchronous wait for a single LB Action. It is
// a per-action safety net only; the create/update attach chains are bounded
// as a whole by lbAggregateTimeout so that "create action + N target + M
// service waits" (each individually up to lbActionTimeout) cannot overrun
// the framework's ~40s per-RPC budget.
const lbActionTimeout = 30 * time.Second

// lbAggregateTimeout bounds the TOTAL synchronous wait for a single RPC's
// LB action chain: the create action (on the create path) plus every
// per-target and per-service attach/reconcile action. Without it, the
// worst case of "create action + one target + one service" already reaches
// 3*lbActionTimeout = 90s, well past the RPC budget. The deadline is
// applied via context.WithDeadline so every wait in the chain aborts as
// soon as it elapses. On elapse after the LB exists, create/update returns
// Success with a warning so the partially-configured LB is retained and
// reconciled on the next Sync.
const lbAggregateTimeout = 35 * time.Second

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

	// Services/Targets are reconciled at create and update. Nil means
	// "not declared in desired state"; an empty slice means "declared as
	// empty" (i.e. clear all). Read-back always populates them with the
	// observed state (possibly an empty slice).
	Services []LoadBalancerServiceProperties `json:"services,omitempty"`
	Targets  []LoadBalancerTargetProperties  `json:"targets,omitempty"`

	// Observed outputs:
	ID   int64  `json:"id,omitempty"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// LoadBalancerServiceProperties mirrors LoadBalancerService in the schema.
// ListenPort is the unique identity of a service within a single LB.
//
// DestinationPort is required by this plugin's schema even though hcloud can
// default it to ListenPort. Keeping it explicit makes desired/read-back shapes
// stable without relying on undocumented nested provider-default hints.
// ProxyProtocol remains a pointer so the mapper can tell omitted apart from
// explicit false.
type LoadBalancerServiceProperties struct {
	Protocol        string                                    `json:"protocol"`
	ListenPort      int                                       `json:"listenPort"`
	DestinationPort *int                                      `json:"destinationPort,omitempty"`
	ProxyProtocol   *bool                                     `json:"proxyProtocol,omitempty"`
	HTTP            *LoadBalancerServiceHTTPProperties        `json:"http,omitempty"`
	HealthCheck     *LoadBalancerServiceHealthCheckProperties `json:"healthCheck,omitempty"`
}

// LoadBalancerServiceHTTPProperties holds HTTP/HTTPS-specific options.
// CookieLifetime / TimeoutIdle are Go time.ParseDuration strings because
// Pkl's Duration type cannot be rendered as JSON.
//
// RedirectHTTP and StickySessions are pointers for the same reason as the
// scalar fields on LoadBalancerServiceProperties: only a pointer can carry
// an explicit `false` distinct from "not declared", which lets update clear
// a provider-side `true` back to `false`.
type LoadBalancerServiceHTTPProperties struct {
	CookieName     string  `json:"cookieName,omitempty"`
	CookieLifetime string  `json:"cookieLifetime,omitempty"`
	CertificateIDs []int64 `json:"certificateIds,omitempty"`
	RedirectHTTP   *bool   `json:"redirectHTTP,omitempty"`
	StickySessions *bool   `json:"stickySessions,omitempty"`
	TimeoutIdle    string  `json:"timeoutIdle,omitempty"`
}

// LoadBalancerServiceHealthCheckProperties configures a service health check.
type LoadBalancerServiceHealthCheckProperties struct {
	Protocol string                                        `json:"protocol"`
	Port     int                                           `json:"port,omitempty"`
	Interval string                                        `json:"interval,omitempty"`
	Timeout  string                                        `json:"timeout,omitempty"`
	Retries  int                                           `json:"retries,omitempty"`
	HTTP     *LoadBalancerServiceHealthCheckHTTPProperties `json:"http,omitempty"`
}

// LoadBalancerServiceHealthCheckHTTPProperties holds HTTP-specific health
// check options. TLS is a pointer so an explicit `false` can be sent to
// clear a provider-side `true` (same rationale as RedirectHTTP/StickySessions).
type LoadBalancerServiceHealthCheckHTTPProperties struct {
	Domain      string   `json:"domain,omitempty"`
	Path        string   `json:"path,omitempty"`
	Response    string   `json:"response,omitempty"`
	StatusCodes []string `json:"statusCodes,omitempty"`
	TLS         *bool    `json:"tls,omitempty"`
}

// LoadBalancerTargetProperties mirrors LoadBalancerTarget in the schema.
// Type determines which of ServerID/Selector/IP is the identity.
type LoadBalancerTargetProperties struct {
	Type         string `json:"type"`
	ServerID     int64  `json:"serverId,omitempty"`
	Selector     string `json:"selector,omitempty"`
	IP           string `json:"ip,omitempty"`
	UsePrivateIP bool   `json:"usePrivateIP,omitempty"`
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
	if err := validateLoadBalancerServices(p.Services); err != nil {
		return nil, err
	}
	if err := validateLoadBalancerTargets(p.Targets); err != nil {
		return nil, err
	}
	return &p, nil
}

// validateLoadBalancerServices enforces the hcloud API constraints the
// parser can catch up-front so create/update surface InvalidRequest rather
// than letting hcloud reject the call: protocol must be one of the declared
// enum values; listen_ports must be unique within the desired state (they're
// the per-LB identity); health-check protocol must be set when a health
// check is declared.
func validateLoadBalancerServices(services []LoadBalancerServiceProperties) error {
	seen := make(map[int]struct{}, len(services))
	for i, s := range services {
		switch s.Protocol {
		case "tcp", "http", "https":
		default:
			return fmt.Errorf("service[%d]: protocol must be one of tcp/http/https (got %q)", i, s.Protocol)
		}
		if s.ListenPort <= 0 {
			return fmt.Errorf("service[%d]: listenPort must be > 0 (got %d)", i, s.ListenPort)
		}
		if _, dup := seen[s.ListenPort]; dup {
			return fmt.Errorf("service[%d]: duplicate listenPort %d (listenPort is the per-LB service identity)", i, s.ListenPort)
		}
		seen[s.ListenPort] = struct{}{}
		if s.DestinationPort == nil {
			return fmt.Errorf("service[%d]: destinationPort is required", i)
		}
		if *s.DestinationPort <= 0 {
			return fmt.Errorf("service[%d]: destinationPort must be > 0 (got %d)", i, *s.DestinationPort)
		}
		if s.HealthCheck != nil {
			switch s.HealthCheck.Protocol {
			case "tcp", "http", "https", "grpc":
			default:
				return fmt.Errorf("service[%d]: healthCheck.protocol must be one of tcp/http/https/grpc (got %q)", i, s.HealthCheck.Protocol)
			}
		}
		// Durations: validate up-front so a typo'd duration surfaces as
		// InvalidRequest rather than failing mid-create.
		if s.HTTP != nil {
			if err := checkDuration("http.cookieLifetime", s.HTTP.CookieLifetime); err != nil {
				return fmt.Errorf("service[%d]: %w", i, err)
			}
			if err := checkDuration("http.timeoutIdle", s.HTTP.TimeoutIdle); err != nil {
				return fmt.Errorf("service[%d]: %w", i, err)
			}
		}
		if s.HealthCheck != nil {
			if err := checkDuration("healthCheck.interval", s.HealthCheck.Interval); err != nil {
				return fmt.Errorf("service[%d]: %w", i, err)
			}
			if err := checkDuration("healthCheck.timeout", s.HealthCheck.Timeout); err != nil {
				return fmt.Errorf("service[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// checkDuration returns nil for an empty string (treated as "unset") and
// otherwise requires the value parse as a Go time.Duration.
func checkDuration(field, raw string) error {
	if raw == "" {
		return nil
	}
	if _, err := time.ParseDuration(raw); err != nil {
		return fmt.Errorf("%s: invalid duration %q (expected Go time.ParseDuration form like \"30s\", \"1h30m\"): %w", field, raw, err)
	}
	return nil
}

// validateLoadBalancerTargets enforces the type/identity coupling declared
// in the schema: exactly one of ServerID/Selector/IP must match the Type.
// Identities must be unique within the desired state — a server-id target
// with the same serverId as another is a programming error hcloud would
// reject as a conflict.
func validateLoadBalancerTargets(targets []LoadBalancerTargetProperties) error {
	seen := make(map[string]struct{}, len(targets))
	for i, t := range targets {
		switch t.Type {
		case "server":
			if t.ServerID == 0 {
				return fmt.Errorf("target[%d]: type=server requires serverId", i)
			}
		case "label_selector":
			if t.Selector == "" {
				return fmt.Errorf("target[%d]: type=label_selector requires selector", i)
			}
		case "ip":
			if t.IP == "" {
				return fmt.Errorf("target[%d]: type=ip requires ip", i)
			}
			if ip := net.ParseIP(t.IP); ip == nil {
				return fmt.Errorf("target[%d]: ip %q is not a valid IP address", i, t.IP)
			}
		default:
			return fmt.Errorf("target[%d]: type must be one of server/label_selector/ip (got %q)", i, t.Type)
		}
		key := loadBalancerTargetKey(t)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("target[%d]: duplicate %s", i, key)
		}
		seen[key] = struct{}{}
		if t.Type == "ip" && t.UsePrivateIP {
			return fmt.Errorf("target[%d]: usePrivateIP is not supported for type=ip", i)
		}
	}
	return nil
}

// loadBalancerTargetKey returns the stable identity of a target used both
// for desired-state validation and for current-vs-desired diffing. The key
// encodes the Type so a server-id and an ip with the same numeric value
// cannot collide.
func loadBalancerTargetKey(t LoadBalancerTargetProperties) string {
	switch t.Type {
	case "server":
		return "server:" + strconv.FormatInt(t.ServerID, 10)
	case "label_selector":
		return "label_selector:" + t.Selector
	case "ip":
		return "ip:" + t.IP
	}
	return t.Type + ":?"
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

	// Fast path: no services/targets declared → defer to the framework's
	// async Status→Success flow. This is the legacy behaviour and keeps the
	// plain LB create as cheap as it was before services/targets landed.
	if len(props.Services) == 0 && len(props.Targets) == 0 {
		return &resource.CreateResult{ProgressResult: progress(
			resource.OperationCreate, resource.OperationStatusInProgress,
			strconv.FormatInt(res.LoadBalancer.ID, 10), strconv.FormatInt(res.Action.ID, 10),
		)}, nil
	}

	// Services/targets can only be added once the LB is up — wait for the
	// create Action to settle synchronously before attaching anything. The
	// whole chain (create action + every per-target/per-service attach) runs
	// under a single aggregate deadline so the worst-case fan-out cannot
	// overrun the framework's per-RPC budget; see lbAggregateTimeout.
	attachCtx, cancel := context.WithDeadline(ctx, time.Now().Add(lbAggregateTimeout))
	defer cancel()
	lbIDStr := strconv.FormatInt(res.LoadBalancer.ID, 10)
	if err := waitForLoadBalancerAction(attachCtx, c, res.Action.ID); err != nil {
		// The LB resource itself was created (we hold its ID); only the
		// post-create Action hasn't settled. Surface Success with a warning
		// rather than Failure so the framework doesn't treat the create as
		// rolled back and orphan the LB. Action errors, API errors, and
		// per-action lbActionTimeout exhaustions still fall through to fail.
		if pr := finishOnAttachTimeout(ctx, c, resource.OperationCreate, lbIDStr,
			fmt.Sprintf("timed out after %s waiting for the create action to settle (LB %d created; pending services/targets complete on the next Sync)", lbAggregateTimeout, res.LoadBalancer.ID), err); pr != nil {
			return &resource.CreateResult{ProgressResult: pr}, nil
		}
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, lbIDStr, "", err.Error(), mapHcloudError(err))}, nil
	}
	if err := applyLoadBalancerServicesAndTargets(attachCtx, c, res.LoadBalancer.ID, props); err != nil {
		if pr := finishOnAttachTimeout(ctx, c, resource.OperationCreate, lbIDStr,
			fmt.Sprintf("timed out attaching services/targets after %s (LB %d created; pending attaches complete on the next Sync)", lbAggregateTimeout, res.LoadBalancer.ID), err); pr != nil {
			return &resource.CreateResult{ProgressResult: pr}, nil
		}
		return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, lbIDStr, "", err.Error(), mapHcloudError(err))}, nil
	}

	// Read back the full LB so the create result carries observed state
	// (services, targets, IPs). The framework's post-create inventory
	// comparison happens before a Sync/Read, so we must populate properties
	// here for the comparison to pass cleanly.
	lb, _, err := c.LoadBalancer().GetByID(ctx, res.LoadBalancer.ID)
	if err != nil || lb == nil {
		// Read-back is best-effort: fall back to the create response rather
		// than hard-failing an otherwise successful create. Any drift is
		// reconciled on the next Sync.
		lb = res.LoadBalancer
	}
	b, _ := json.Marshal(loadBalancerPropertiesFrom(lb))
	pr := progress(resource.OperationCreate, resource.OperationStatusSuccess, strconv.FormatInt(lb.ID, 10), "")
	pr.ResourceProperties = b
	return &resource.CreateResult{ProgressResult: pr}, nil
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

// update changes mutable fields (name, labels) and reconciles services and
// targets against the current observed state. Load Balancer type, location,
// network zone, and algorithm are immutable after creation in this handler.
//
// Services are reconciled by listenPort: existing ports not in desired are
// deleted; new ports are added; ports present in both are updated in place.
// Targets are reconciled by identity (see loadBalancerTargetKey): existing
// identities absent from desired are removed; new identities are added;
// identities with changed attach-only attributes such as usePrivateIP are
// removed and re-added.
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

	// Reconcile services/targets only when they're declared in the desired
	// state. A nil slice means "leave untouched" (preserves backwards
	// compatibility with forms that only set name/labels); an empty slice
	// means "clear all".
	if desired.Services != nil || desired.Targets != nil {
		lb, _, err := c.LoadBalancer().GetByID(ctx, id)
		if err != nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", fmt.Sprintf("read-back for reconcile failed: %v", err), mapHcloudError(err))}, nil
		}
		if lb == nil {
			return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, req.NativeID, "", "load balancer not found", resource.OperationErrorCodeNotFound)}, nil
		}
		reconcile := *desired
		if desired.Services == nil {
			reconcile.Services = loadBalancerServicesFrom(lb)
		}
		if desired.Targets == nil {
			reconcile.Targets = loadBalancerTargetsFrom(lb)
		}
		// The reconcile fans out one action per added/removed/updated target
		// and service; bound the total under a single deadline so a large
		// diff cannot overrun the framework's per-RPC budget.
		reconcileCtx, cancel := context.WithDeadline(ctx, time.Now().Add(lbAggregateTimeout))
		defer cancel()
		if err := reconcileLoadBalancer(reconcileCtx, c, lb, &reconcile); err != nil {
			// The LB exists throughout update; only the per-service/per-target
			// reconcile fan-out was truncated. Surface Success with a warning
			// so the next Sync picks up the remaining attaches — returning
			// Failure would imply the LB itself failed to update.
			if pr := finishOnAttachTimeout(ctx, c, resource.OperationUpdate, req.NativeID,
				fmt.Sprintf("timed out reconciling services/targets after %s; partial changes will be completed on the next Sync", lbAggregateTimeout), err); pr != nil {
				return &resource.UpdateResult{ProgressResult: pr}, nil
			}
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

// applyLoadBalancerServicesAndTargets is the create-time attach path: it
// adds every declared service and target in declaration order, waiting for
// each Action to settle inline. Unlike update, it does not diff — there is
// no existing state to diff against on a freshly created LB.
func applyLoadBalancerServicesAndTargets(ctx context.Context, c hcloudAPI, lbID int64, desired *LoadBalancerProperties) error {
	lb := &hcloud.LoadBalancer{ID: lbID}
	for _, t := range desired.Targets {
		action, err := addLoadBalancerTarget(ctx, c, lb, t)
		if err != nil {
			return fmt.Errorf("add target %s: %w", loadBalancerTargetKey(t), err)
		}
		// addLoadBalancerTarget surfaces a nil Action on success-with-no-action
		// (the same shape the AddService loop handles below); guard it so a
		// nil dereference here cannot crash the plugin mid-create.
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for add target %s: %w", loadBalancerTargetKey(t), err)
		}
	}
	for _, s := range desired.Services {
		action, _, err := c.LoadBalancer().AddService(ctx, lb, loadBalancerAddServiceOpts(s))
		if err != nil {
			return fmt.Errorf("add service on listenPort %d: %w", s.ListenPort, err)
		}
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for add service on listenPort %d: %w", s.ListenPort, err)
		}
	}
	return nil
}

// reconcileLoadBalancer is the update-time diff/reconcile path for services
// and targets. It mutates the LB to match desired.
func reconcileLoadBalancer(ctx context.Context, c hcloudAPI, current *hcloud.LoadBalancer, desired *LoadBalancerProperties) error {
	if err := reconcileLoadBalancerTargets(ctx, c, current, desired.Targets); err != nil {
		return err
	}
	return reconcileLoadBalancerServices(ctx, c, current, desired.Services)
}

func reconcileLoadBalancerTargets(ctx context.Context, c hcloudAPI, current *hcloud.LoadBalancer, desired []LoadBalancerTargetProperties) error {
	desiredByKey := make(map[string]LoadBalancerTargetProperties, len(desired))
	for _, t := range desired {
		desiredByKey[loadBalancerTargetKey(t)] = t
	}
	replaceByKey := make(map[string]LoadBalancerTargetProperties)
	// Remove targets present in current but not in desired. Removal order
	// does not matter; removal calls return Actions that must be awaited
	// serially to avoid hcloud per-LB concurrency conflicts.
	for _, t := range current.Targets {
		currentKey := loadBalancerTargetKeyFromHcloud(t)
		desiredTarget, keep := desiredByKey[currentKey]
		if keep && !loadBalancerTargetNeedsReplace(t, desiredTarget) {
			continue
		}
		if keep {
			replaceByKey[currentKey] = desiredTarget
		}
		action, err := removeLoadBalancerTarget(ctx, c, current, t)
		if err != nil {
			return fmt.Errorf("remove target %s: %w", currentKey, err)
		}
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for remove target %s: %w", currentKey, err)
		}
	}
	// Add targets present in desired but missing from current. Add calls
	// likewise return Actions and must be awaited serially.
	currentByKey := make(map[string]struct{}, len(current.Targets))
	for _, t := range current.Targets {
		currentByKey[loadBalancerTargetKeyFromHcloud(t)] = struct{}{}
	}
	for _, t := range desired {
		key := loadBalancerTargetKey(t)
		if _, exists := currentByKey[key]; exists {
			// hcloud exposes no target update call. Attributes modeled on the
			// attach operation, such as usePrivateIP, are changed by detaching
			// and re-attaching the same target identity.
			if _, replace := replaceByKey[key]; !replace {
				continue
			}
		}
		action, err := addLoadBalancerTarget(ctx, c, current, t)
		if err != nil {
			return fmt.Errorf("add target %s: %w", key, err)
		}
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for add target %s: %w", key, err)
		}
	}
	return nil
}

func loadBalancerTargetNeedsReplace(current hcloud.LoadBalancerTarget, desired LoadBalancerTargetProperties) bool {
	switch current.Type {
	case hcloud.LoadBalancerTargetTypeServer, hcloud.LoadBalancerTargetTypeLabelSelector:
		return current.UsePrivateIP != desired.UsePrivateIP
	default:
		return false
	}
}

func reconcileLoadBalancerServices(ctx context.Context, c hcloudAPI, current *hcloud.LoadBalancer, desired []LoadBalancerServiceProperties) error {
	currentByListenPort := make(map[int]hcloud.LoadBalancerService, len(current.Services))
	for _, s := range current.Services {
		currentByListenPort[s.ListenPort] = s
	}
	desiredByListenPort := make(map[int]LoadBalancerServiceProperties, len(desired))
	for _, s := range desired {
		desiredByListenPort[s.ListenPort] = s
	}
	lb := &hcloud.LoadBalancer{ID: current.ID}

	// Delete services present in current but not in desired. hcloud requires
	// all other service mutations to land before additions for the same
	// listenPort; doing deletes first keeps ordering simple.
	for listenPort, currentSvc := range currentByListenPort {
		if _, keep := desiredByListenPort[listenPort]; keep {
			continue
		}
		action, _, err := c.LoadBalancer().DeleteService(ctx, lb, currentSvc.ListenPort)
		if err != nil {
			return fmt.Errorf("delete service on listenPort %d: %w", listenPort, err)
		}
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for delete service on listenPort %d: %w", listenPort, err)
		}
	}
	// Update services present in both, add services only in desired.
	for listenPort, desiredSvc := range desiredByListenPort {
		if _, exists := currentByListenPort[listenPort]; exists {
			action, _, err := c.LoadBalancer().UpdateService(ctx, lb, listenPort, loadBalancerUpdateServiceOpts(desiredSvc))
			if err != nil {
				return fmt.Errorf("update service on listenPort %d: %w", listenPort, err)
			}
			if action == nil {
				continue
			}
			if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
				return fmt.Errorf("wait for update service on listenPort %d: %w", listenPort, err)
			}
			continue
		}
		action, _, err := c.LoadBalancer().AddService(ctx, lb, loadBalancerAddServiceOpts(desiredSvc))
		if err != nil {
			return fmt.Errorf("add service on listenPort %d: %w", listenPort, err)
		}
		if action == nil {
			continue
		}
		if err := waitForLoadBalancerAction(ctx, c, action.ID); err != nil {
			return fmt.Errorf("wait for add service on listenPort %d: %w", listenPort, err)
		}
	}
	return nil
}

// addLoadBalancerTarget dispatches an Add*Target call based on the target's
// declared type.
func addLoadBalancerTarget(ctx context.Context, c hcloudAPI, lb *hcloud.LoadBalancer, t LoadBalancerTargetProperties) (*hcloud.Action, error) {
	switch t.Type {
	case "server":
		opts := hcloud.LoadBalancerAddServerTargetOpts{
			Server:       &hcloud.Server{ID: t.ServerID},
			UsePrivateIP: hcloud.Ptr(t.UsePrivateIP),
		}
		action, _, err := c.LoadBalancer().AddServerTarget(ctx, lb, opts)
		return action, err
	case "label_selector":
		opts := hcloud.LoadBalancerAddLabelSelectorTargetOpts{
			Selector:     t.Selector,
			UsePrivateIP: hcloud.Ptr(t.UsePrivateIP),
		}
		action, _, err := c.LoadBalancer().AddLabelSelectorTarget(ctx, lb, opts)
		return action, err
	case "ip":
		ip := net.ParseIP(t.IP)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip %q", t.IP)
		}
		action, _, err := c.LoadBalancer().AddIPTarget(ctx, lb, hcloud.LoadBalancerAddIPTargetOpts{IP: ip})
		return action, err
	}
	return nil, fmt.Errorf("unsupported target type %q", t.Type)
}

// removeLoadBalancerTarget dispatches the matching Remove*Target call for an
// observed hcloud LoadBalancerTarget.
func removeLoadBalancerTarget(ctx context.Context, c hcloudAPI, lb *hcloud.LoadBalancer, t hcloud.LoadBalancerTarget) (*hcloud.Action, error) {
	switch t.Type {
	case hcloud.LoadBalancerTargetTypeServer:
		if t.Server == nil || t.Server.Server == nil {
			return nil, errors.New("server target missing server reference")
		}
		action, _, err := c.LoadBalancer().RemoveServerTarget(ctx, lb, t.Server.Server)
		return action, err
	case hcloud.LoadBalancerTargetTypeLabelSelector:
		if t.LabelSelector == nil {
			return nil, errors.New("label_selector target missing selector reference")
		}
		action, _, err := c.LoadBalancer().RemoveLabelSelectorTarget(ctx, lb, t.LabelSelector.Selector)
		return action, err
	case hcloud.LoadBalancerTargetTypeIP:
		if t.IP == nil {
			return nil, errors.New("ip target missing ip reference")
		}
		ip := net.ParseIP(t.IP.IP)
		if ip == nil {
			return nil, fmt.Errorf("observed ip target %q is not a valid IP", t.IP.IP)
		}
		action, _, err := c.LoadBalancer().RemoveIPTarget(ctx, lb, ip)
		return action, err
	}
	return nil, fmt.Errorf("unsupported observed target type %q", t.Type)
}

// loadBalancerAddServiceOpts maps the desired-state service shape to the
// hcloud AddService opts. Pointer-valued fields are populated from the
// desired state's omitempty-zero values, so a desired service with no HTTP
// block produces a nil HTTP pointer (not a pointer to a zero-struct).
func loadBalancerAddServiceOpts(s LoadBalancerServiceProperties) hcloud.LoadBalancerAddServiceOpts {
	opts := hcloud.LoadBalancerAddServiceOpts{
		Protocol:   hcloud.LoadBalancerServiceProtocol(s.Protocol),
		ListenPort: hcloud.Ptr(s.ListenPort),
	}
	// DestinationPort is required by validation and schema, but keep the
	// nil guard so direct unit-level calls cannot panic.
	if s.DestinationPort != nil {
		opts.DestinationPort = hcloud.Ptr(*s.DestinationPort)
	}
	// ProxyProtocol is optional; omitting lets hcloud apply its default
	// (false), while an explicit false still reaches update/create.
	if s.ProxyProtocol != nil {
		opts.Proxyprotocol = hcloud.Ptr(*s.ProxyProtocol)
	}
	if s.HTTP != nil {
		opts.HTTP = loadBalancerAddServiceOptsHTTP(*s.HTTP)
	}
	if s.HealthCheck != nil {
		opts.HealthCheck = loadBalancerAddServiceOptsHealthCheck(*s.HealthCheck)
	}
	return opts
}

func loadBalancerAddServiceOptsHTTP(h LoadBalancerServiceHTTPProperties) *hcloud.LoadBalancerAddServiceOptsHTTP {
	out := &hcloud.LoadBalancerAddServiceOptsHTTP{}
	if h.CookieName != "" {
		out.CookieName = hcloud.Ptr(h.CookieName)
	}
	if d, err := time.ParseDuration(h.CookieLifetime); err == nil {
		out.CookieLifetime = hcloud.Ptr(d)
	}
	if h.RedirectHTTP != nil {
		out.RedirectHTTP = hcloud.Ptr(*h.RedirectHTTP)
	}
	if h.StickySessions != nil {
		out.StickySessions = hcloud.Ptr(*h.StickySessions)
	}
	if d, err := time.ParseDuration(h.TimeoutIdle); err == nil {
		out.TimeoutIdle = hcloud.Ptr(d)
	}
	if len(h.CertificateIDs) > 0 {
		out.Certificates = make([]*hcloud.Certificate, 0, len(h.CertificateIDs))
		for _, id := range h.CertificateIDs {
			out.Certificates = append(out.Certificates, &hcloud.Certificate{ID: id})
		}
	}
	return out
}

func loadBalancerAddServiceOptsHealthCheck(hc LoadBalancerServiceHealthCheckProperties) *hcloud.LoadBalancerAddServiceOptsHealthCheck {
	out := &hcloud.LoadBalancerAddServiceOptsHealthCheck{
		Protocol: hcloud.LoadBalancerServiceProtocol(hc.Protocol),
	}
	if hc.Port != 0 {
		out.Port = hcloud.Ptr(hc.Port)
	}
	if d, err := time.ParseDuration(hc.Interval); err == nil {
		out.Interval = hcloud.Ptr(d)
	}
	if d, err := time.ParseDuration(hc.Timeout); err == nil {
		out.Timeout = hcloud.Ptr(d)
	}
	if hc.Retries != 0 {
		out.Retries = hcloud.Ptr(hc.Retries)
	}
	if hc.HTTP != nil {
		out.HTTP = loadBalancerAddServiceOptsHealthCheckHTTP(*hc.HTTP)
	}
	return out
}

func loadBalancerAddServiceOptsHealthCheckHTTP(h LoadBalancerServiceHealthCheckHTTPProperties) *hcloud.LoadBalancerAddServiceOptsHealthCheckHTTP {
	out := &hcloud.LoadBalancerAddServiceOptsHealthCheckHTTP{}
	if h.Domain != "" {
		out.Domain = hcloud.Ptr(h.Domain)
	}
	if h.Path != "" {
		out.Path = hcloud.Ptr(h.Path)
	}
	if h.Response != "" {
		out.Response = hcloud.Ptr(h.Response)
	}
	if len(h.StatusCodes) > 0 {
		out.StatusCodes = append([]string(nil), h.StatusCodes...)
	}
	if h.TLS != nil {
		out.TLS = hcloud.Ptr(*h.TLS)
	}
	return out
}

// loadBalancerUpdateServiceOpts is the update counterpart of
// loadBalancerAddServiceOpts. hcloud's UpdateService does not take a
// ListenPort (it is the URL path param); every other field maps 1:1.
func loadBalancerUpdateServiceOpts(s LoadBalancerServiceProperties) hcloud.LoadBalancerUpdateServiceOpts {
	opts := hcloud.LoadBalancerUpdateServiceOpts{
		Protocol: hcloud.LoadBalancerServiceProtocol(s.Protocol),
	}
	if s.DestinationPort != nil {
		opts.DestinationPort = hcloud.Ptr(*s.DestinationPort)
	}
	if s.ProxyProtocol != nil {
		opts.Proxyprotocol = hcloud.Ptr(*s.ProxyProtocol)
	}
	if s.HTTP != nil {
		opts.HTTP = loadBalancerUpdateServiceOptsHTTP(*s.HTTP)
	}
	if s.HealthCheck != nil {
		opts.HealthCheck = loadBalancerUpdateServiceOptsHealthCheck(*s.HealthCheck)
	}
	return opts
}

func loadBalancerUpdateServiceOptsHTTP(h LoadBalancerServiceHTTPProperties) *hcloud.LoadBalancerUpdateServiceOptsHTTP {
	out := &hcloud.LoadBalancerUpdateServiceOptsHTTP{}
	if h.CookieName != "" {
		out.CookieName = hcloud.Ptr(h.CookieName)
	}
	if d, err := time.ParseDuration(h.CookieLifetime); err == nil {
		out.CookieLifetime = hcloud.Ptr(d)
	}
	if h.RedirectHTTP != nil {
		out.RedirectHTTP = hcloud.Ptr(*h.RedirectHTTP)
	}
	if h.StickySessions != nil {
		out.StickySessions = hcloud.Ptr(*h.StickySessions)
	}
	if d, err := time.ParseDuration(h.TimeoutIdle); err == nil {
		out.TimeoutIdle = hcloud.Ptr(d)
	}
	if len(h.CertificateIDs) > 0 {
		out.Certificates = make([]*hcloud.Certificate, 0, len(h.CertificateIDs))
		for _, id := range h.CertificateIDs {
			out.Certificates = append(out.Certificates, &hcloud.Certificate{ID: id})
		}
	}
	return out
}

func loadBalancerUpdateServiceOptsHealthCheck(hc LoadBalancerServiceHealthCheckProperties) *hcloud.LoadBalancerUpdateServiceOptsHealthCheck {
	out := &hcloud.LoadBalancerUpdateServiceOptsHealthCheck{
		Protocol: hcloud.LoadBalancerServiceProtocol(hc.Protocol),
	}
	if hc.Port != 0 {
		out.Port = hcloud.Ptr(hc.Port)
	}
	if d, err := time.ParseDuration(hc.Interval); err == nil {
		out.Interval = hcloud.Ptr(d)
	}
	if d, err := time.ParseDuration(hc.Timeout); err == nil {
		out.Timeout = hcloud.Ptr(d)
	}
	if hc.Retries != 0 {
		out.Retries = hcloud.Ptr(hc.Retries)
	}
	if hc.HTTP != nil {
		out.HTTP = loadBalancerUpdateServiceOptsHealthCheckHTTP(*hc.HTTP)
	}
	return out
}

func loadBalancerUpdateServiceOptsHealthCheckHTTP(h LoadBalancerServiceHealthCheckHTTPProperties) *hcloud.LoadBalancerUpdateServiceOptsHealthCheckHTTP {
	out := &hcloud.LoadBalancerUpdateServiceOptsHealthCheckHTTP{}
	if h.Domain != "" {
		out.Domain = hcloud.Ptr(h.Domain)
	}
	if h.Path != "" {
		out.Path = hcloud.Ptr(h.Path)
	}
	if h.Response != "" {
		out.Response = hcloud.Ptr(h.Response)
	}
	if len(h.StatusCodes) > 0 {
		out.StatusCodes = append([]string(nil), h.StatusCodes...)
	}
	if h.TLS != nil {
		out.TLS = hcloud.Ptr(*h.TLS)
	}
	return out
}

// finishOnAttachTimeout returns a Success ProgressResult carrying the LB's
// best-effort observed state when an attach/reconcile chain was aborted by
// the aggregate deadline. Returning Success (rather than Failure) is
// deliberate: at this point the LB resource itself exists in hcloud — only
// the per-service/per-target attaches are partial — so Failure would
// mislead the formae agent into treating the operation as rolled back and
// orphaning the LB (or marking the update failed despite a partial apply).
// Best-effort read-back populates the result so the framework's inventory
// comparison can pass; the next Sync reconciles any remaining drift.
//
// Returns nil when err is NOT a context.DeadlineExceeded, so callers can
// fall through to the loud Failure path for Action errors, API errors, and
// per-action lbActionTimeout exhaustions (which all indicate something
// other than "the chain simply ran out of aggregate budget").
func finishOnAttachTimeout(ctx context.Context, c hcloudAPI, op resource.Operation, nativeID, msg string, err error) *resource.ProgressResult {
	if !errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	pr := progress(op, resource.OperationStatusSuccess, nativeID, "")
	pr.StatusMessage = msg
	if id, parseErr := parseNativeID(nativeID); parseErr == nil {
		if lb, _, getErr := c.LoadBalancer().GetByID(ctx, id); getErr == nil && lb != nil {
			if b, marshalErr := json.Marshal(loadBalancerPropertiesFrom(lb)); marshalErr == nil {
				pr.ResourceProperties = b
			}
		}
	}
	return pr
}

// waitForLoadBalancerAction polls an hcloud Action until it leaves the
// running state. Bounds the wait at lbActionTimeout so a stuck action fails
// the create/update loudly rather than hanging the framework's RPC budget.
// Returns the action's error message if the action ended in error.
func waitForLoadBalancerAction(ctx context.Context, c hcloudAPI, actionID int64) error {
	// Per-action safety net; the chain-level bound comes from the deadline
	// context supplied by the create/update handlers (see lbAggregateTimeout).
	deadline := time.Now().Add(lbActionTimeout)
	timer := time.NewTimer(lbActionPollInterval)
	defer timer.Stop()
	for {
		action, _, err := c.Action().GetByID(ctx, actionID)
		if err != nil {
			return err
		}
		if action == nil {
			return fmt.Errorf("action %d not found", actionID)
		}
		switch action.Status {
		case hcloud.ActionStatusSuccess:
			return nil
		case hcloud.ActionStatusError:
			if action.ErrorMessage == "" {
				return fmt.Errorf("action %d ended in error", actionID)
			}
			return errors.New(action.ErrorMessage)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("action %d timed out after %s", actionID, lbActionTimeout)
		}
		// Wait for the next poll tick OR ctx cancellation, whichever is first,
		// so a chain-level deadline aborts this waiter within one poll rather
		// than sleeping through it.
		timer.Reset(lbActionPollInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
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
	props.Services = loadBalancerServicesFrom(lb)
	props.Targets = loadBalancerTargetsFrom(lb)
	return props
}

func loadBalancerServicesFrom(lb *hcloud.LoadBalancer) []LoadBalancerServiceProperties {
	services := make([]LoadBalancerServiceProperties, 0, len(lb.Services))
	for _, s := range lb.Services {
		p := LoadBalancerServiceProperties{
			Protocol:   string(s.Protocol),
			ListenPort: s.ListenPort,
		}
		// Round-trip stability: destinationPort is required in desired state,
		// so read-back always surfaces hcloud's actual destination port.
		if s.DestinationPort != 0 {
			p.DestinationPort = hcloud.Ptr(s.DestinationPort)
		}
		p.ProxyProtocol = hcloud.Ptr(s.Proxyprotocol)
		if s.Protocol == hcloud.LoadBalancerServiceProtocolHTTP || s.Protocol == hcloud.LoadBalancerServiceProtocolHTTPS || s.HTTP.Certificates != nil || s.HTTP.CookieName != "" || s.HTTP.RedirectHTTP || s.HTTP.StickySessions || s.HTTP.TimeoutIdle > 0 || s.HTTP.CookieLifetime > 0 {
			http := &LoadBalancerServiceHTTPProperties{
				CookieName:     s.HTTP.CookieName,
				CookieLifetime: durationString(s.HTTP.CookieLifetime),
				RedirectHTTP:   hcloud.Ptr(s.HTTP.RedirectHTTP),
				StickySessions: hcloud.Ptr(s.HTTP.StickySessions),
				TimeoutIdle:    durationString(s.HTTP.TimeoutIdle),
			}
			p.HTTP = http
			if len(s.HTTP.Certificates) > 0 {
				p.HTTP.CertificateIDs = make([]int64, 0, len(s.HTTP.Certificates))
				for _, cert := range s.HTTP.Certificates {
					p.HTTP.CertificateIDs = append(p.HTTP.CertificateIDs, cert.ID)
				}
			}
		}
		if s.HealthCheck.Protocol != "" {
			p.HealthCheck = &LoadBalancerServiceHealthCheckProperties{
				Protocol: string(s.HealthCheck.Protocol),
				Port:     s.HealthCheck.Port,
				Interval: durationString(s.HealthCheck.Interval),
				Timeout:  durationString(s.HealthCheck.Timeout),
				Retries:  s.HealthCheck.Retries,
			}
			if s.HealthCheck.HTTP != nil {
				hcHTTP := &LoadBalancerServiceHealthCheckHTTPProperties{
					Domain:      s.HealthCheck.HTTP.Domain,
					Path:        s.HealthCheck.HTTP.Path,
					Response:    s.HealthCheck.HTTP.Response,
					StatusCodes: append([]string(nil), s.HealthCheck.HTTP.StatusCodes...),
					TLS:         hcloud.Ptr(s.HealthCheck.HTTP.TLS),
				}
				p.HealthCheck.HTTP = hcHTTP
			}
		}
		services = append(services, p)
	}
	return services
}

func loadBalancerTargetsFrom(lb *hcloud.LoadBalancer) []LoadBalancerTargetProperties {
	targets := make([]LoadBalancerTargetProperties, 0, len(lb.Targets))
	for _, t := range lb.Targets {
		p := LoadBalancerTargetProperties{
			Type:         string(t.Type),
			UsePrivateIP: t.UsePrivateIP,
		}
		switch t.Type {
		case hcloud.LoadBalancerTargetTypeServer:
			if t.Server != nil && t.Server.Server != nil {
				p.ServerID = t.Server.Server.ID
			}
		case hcloud.LoadBalancerTargetTypeLabelSelector:
			if t.LabelSelector != nil {
				p.Selector = t.LabelSelector.Selector
			}
		case hcloud.LoadBalancerTargetTypeIP:
			if t.IP != nil {
				p.IP = t.IP.IP
			}
		}
		targets = append(targets, p)
	}
	return targets
}

// loadBalancerTargetKeyFromHcloud is the observed-state counterpart of
// loadBalancerTargetKey. It MUST produce the same string for the same
// identity, otherwise the update-time diff will mis-classify targets as
// added/removed and churn them.
func loadBalancerTargetKeyFromHcloud(t hcloud.LoadBalancerTarget) string {
	switch t.Type {
	case hcloud.LoadBalancerTargetTypeServer:
		if t.Server == nil || t.Server.Server == nil {
			return "server:?"
		}
		return "server:" + strconv.FormatInt(t.Server.Server.ID, 10)
	case hcloud.LoadBalancerTargetTypeLabelSelector:
		if t.LabelSelector == nil {
			return "label_selector:?"
		}
		return "label_selector:" + t.LabelSelector.Selector
	case hcloud.LoadBalancerTargetTypeIP:
		if t.IP == nil {
			return "ip:?"
		}
		return "ip:" + t.IP.IP
	}
	return string(t.Type) + ":?"
}

// durationString renders a time.Duration as the Go time.ParseDuration form
// (e.g. "30s", "1h30m"). Used for observed-state round-tripping of HTTP and
// health-check durations; the formae agent serializes desired-state durations
// as the same form, so this keeps observed-vs-desired diffing stable.
func durationString(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}
