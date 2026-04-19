package acp

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestAutoApproveOutcome_PrefersAllowAlways(t *testing.T) {
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways},
			{OptionId: "reject", Kind: acp.PermissionOptionKindRejectOnce},
		},
	}
	out := AutoApproveOutcome(req)
	if out.Selected == nil {
		t.Fatalf("expected Selected outcome, got %+v", out)
	}
	if string(out.Selected.OptionId) != "always" {
		t.Errorf("OptionId = %q, want always", out.Selected.OptionId)
	}
}

func TestAutoApproveOutcome_FallsBackToAllowOnce(t *testing.T) {
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "reject-always", Kind: acp.PermissionOptionKindRejectAlways},
			{OptionId: "once", Kind: acp.PermissionOptionKindAllowOnce},
		},
	}
	out := AutoApproveOutcome(req)
	if out.Selected == nil || string(out.Selected.OptionId) != "once" {
		t.Errorf("expected once, got %+v", out)
	}
}

func TestAutoApproveOutcome_NoOptionsCancels(t *testing.T) {
	out := AutoApproveOutcome(acp.RequestPermissionRequest{})
	if out.Cancelled == nil { //nolint:misspell // SDK field uses British spelling
		t.Errorf("expected cancel outcome, got %+v", out)
	}
}

func TestAutoApproveOutcome_NoAllowPicksFirst(t *testing.T) {
	req := acp.RequestPermissionRequest{
		Options: []acp.PermissionOption{
			{OptionId: "reject-once", Kind: acp.PermissionOptionKindRejectOnce},
			{OptionId: "reject-always", Kind: acp.PermissionOptionKindRejectAlways},
		},
	}
	out := AutoApproveOutcome(req)
	if out.Selected == nil || string(out.Selected.OptionId) != "reject-once" {
		t.Errorf("expected first option as fallback, got %+v", out)
	}
}
