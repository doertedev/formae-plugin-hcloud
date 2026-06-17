// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// This file locks down the pure helpers added to plugin.go: the hcloud error
// code mapper, the managed-label merge/strip pair, the NativeID parser, and
// the malformed-config surfacing in resolveToken. They are the contract every
// per-resource handler will rely on once wired up, so each behaviour is
// pinned explicitly (including non-mutation of caller-owned maps).

// --- mapHcloudError --------------------------------------------------------

func TestMapHcloudError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want resource.OperationErrorCode
	}{
		{"nil error -> InternalFailure", nil, resource.OperationErrorCodeInternalFailure},
		{"unauthorized -> InvalidCredentials", hcloud.Error{Code: hcloud.ErrorCodeUnauthorized, Message: "bad token"}, resource.OperationErrorCodeInvalidCredentials},
		{"forbidden -> AccessDenied", hcloud.Error{Code: hcloud.ErrorCodeForbidden, Message: "no"}, resource.OperationErrorCodeAccessDenied},
		{"token_readonly -> AccessDenied", hcloud.Error{Code: hcloud.ErrorCodeTokenReadonly, Message: "ro"}, resource.OperationErrorCodeAccessDenied},
		{"rate_limit_exceeded -> Throttling", hcloud.Error{Code: hcloud.ErrorCodeRateLimitExceeded, Message: "slow"}, resource.OperationErrorCodeThrottling},
		{"not_found -> NotFound", hcloud.Error{Code: hcloud.ErrorCodeNotFound, Message: "gone"}, resource.OperationErrorCodeNotFound},
		{"invalid_input -> InvalidRequest", hcloud.Error{Code: hcloud.ErrorCodeInvalidInput, Message: "bad"}, resource.OperationErrorCodeInvalidRequest},
		{"conflict -> ResourceConflict", hcloud.Error{Code: hcloud.ErrorCodeConflict, Message: "chg"}, resource.OperationErrorCodeResourceConflict},
		{"locked -> ResourceConflict", hcloud.Error{Code: hcloud.ErrorCodeLocked, Message: "lk"}, resource.OperationErrorCodeResourceConflict},
		{"resource_limit_exceeded -> ServiceLimitExceeded", hcloud.Error{Code: hcloud.ErrorCodeResourceLimitExceeded, Message: "cap"}, resource.OperationErrorCodeServiceLimitExceeded},
		{"timeout -> ServiceTimeout", hcloud.Error{Code: hcloud.ErrorCodeTimeout, Message: "tm"}, resource.OperationErrorCodeServiceTimeout},
		{"plain non-hcloud error -> ServiceInternalError", errors.New("boom"), resource.OperationErrorCodeServiceInternalError},
		{"unknown hcloud code -> ServiceInternalError", hcloud.Error{Code: hcloud.ErrorCodeServiceError, Message: "?"}, resource.OperationErrorCodeServiceInternalError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapHcloudError(c.err)
			if got != c.want {
				t.Errorf("mapHcloudError(%v): want %q, got %q", c.err, c.want, got)
			}
		})
	}
}

// --- mergeManagedLabels ----------------------------------------------------

func TestMergeManagedLabels(t *testing.T) {
	t.Run("nil input yields only managed_by", func(t *testing.T) {
		got := mergeManagedLabels(nil)
		want := map[string]string{managedByLabelKey: managedByLabelValue}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mergeManagedLabels(nil): want %v, got %v", want, got)
		}
	})

	t.Run("empty input yields only managed_by", func(t *testing.T) {
		got := mergeManagedLabels(map[string]string{})
		want := map[string]string{managedByLabelKey: managedByLabelValue}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mergeManagedLabels(empty): want %v, got %v", want, got)
		}
	})

	t.Run("user labels are preserved and managed_by added", func(t *testing.T) {
		input := map[string]string{"owner": "x", "env": "prod"}
		got := mergeManagedLabels(input)
		want := map[string]string{"owner": "x", "env": "prod", managedByLabelKey: managedByLabelValue}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("mergeManagedLabels: want %v, got %v", want, got)
		}
	})

	t.Run("does not mutate the caller's map", func(t *testing.T) {
		input := map[string]string{"owner": "x"}
		snapshot := map[string]string{"owner": "x"}
		_ = mergeManagedLabels(input)
		if !reflect.DeepEqual(input, snapshot) {
			t.Errorf("mergeManagedLabels mutated input: want %v, got %v", snapshot, input)
		}
	})

	t.Run("user-supplied managed_by is overwritten", func(t *testing.T) {
		input := map[string]string{"owner": "x", managedByLabelKey: "attacker"}
		got := mergeManagedLabels(input)
		if got[managedByLabelKey] != managedByLabelValue {
			t.Errorf("expected managed_by=%q, got %q", managedByLabelValue, got[managedByLabelKey])
		}
		if got["owner"] != "x" {
			t.Errorf("expected owner=x preserved, got %q", got["owner"])
		}
		if len(got) != 2 {
			t.Errorf("expected exactly 2 labels, got %d (%v)", len(got), got)
		}
	})
}

// --- stripManagedLabel -----------------------------------------------------

func TestStripManagedLabel(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]string
		want  map[string]string
	}{
		{"nil -> nil", nil, nil},
		{"empty -> nil", map[string]string{}, nil},
		{"only managed_by -> nil", map[string]string{managedByLabelKey: managedByLabelValue}, nil},
		{"managed_by plus owner -> owner only", map[string]string{managedByLabelKey: managedByLabelValue, "owner": "x"}, map[string]string{"owner": "x"}},
		{"owner only -> owner only", map[string]string{"owner": "x"}, map[string]string{"owner": "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripManagedLabel(c.input)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("stripManagedLabel: want %v, got %v", c.want, got)
			}
		})
	}

	t.Run("does not mutate the caller's map", func(t *testing.T) {
		input := map[string]string{managedByLabelKey: managedByLabelValue, "owner": "x"}
		snapshot := map[string]string{managedByLabelKey: managedByLabelValue, "owner": "x"}
		_ = stripManagedLabel(input)
		if !reflect.DeepEqual(input, snapshot) {
			t.Errorf("stripManagedLabel mutated input: want %v, got %v", snapshot, input)
		}
	})
}

// --- parseNativeID ---------------------------------------------------------

func TestParseNativeID(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"valid 123", "123", 123, false},
		{"valid large", "9223372036854775807", 9223372036854775807, false},
		{"empty", "", 0, true},
		{"non-numeric", "abc", 0, true},
		{"float-like", "1.5", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseNativeID(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseNativeID(%q): expected error, got nil", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNativeID(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("parseNativeID(%q): want %d, got %d", c.in, c.want, got)
			}
		})
	}
}

// --- resolveToken: malformed JSON now surfaces -----------------------------

func TestResolveToken_MalformedConfigNowErrors(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	_, err := resolveToken(json.RawMessage(`{not json`))
	if err == nil {
		t.Fatal("expected error for malformed target config JSON, got nil")
	}
}

func TestResolveToken_ConfigObjectWins(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	tok, err := resolveToken(json.RawMessage(`{"token":"abc"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "abc" {
		t.Fatalf("expected abc, got %q", tok)
	}
}

func TestResolveToken_EmptyTokenFallsThroughToEnv(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	tok, err := resolveToken(json.RawMessage(`{"token":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "envtok" {
		t.Fatalf("expected envtok fallback, got %q", tok)
	}
}

func TestResolveToken_NilInputWithEnvToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "envtok")
	tok, err := resolveToken(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "envtok" {
		t.Fatalf("expected envtok, got %q", tok)
	}
}

func TestResolveToken_NilInputWithoutEnvToken(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	_, err := resolveToken(nil)
	if err == nil {
		t.Fatal("expected error when no token is configured anywhere")
	}
}
