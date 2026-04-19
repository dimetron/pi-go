package acp

import (
	acp "github.com/coder/acp-go-sdk"
)

// AutoApproveOutcome picks the "best" allow option from a permission request
// and returns a Selected outcome for it. Preference order is allow_always >
// allow_once > any other option whose Kind starts with "allow_". If no allow
// option is present, the first option is selected so the agent can make
// forward progress; if the request carries no options at all, the request is
// canceled.
//
// pi-go ACP subagents (claude / gemini / cursor) run inside a single user-
// initiated session that has already been authorized at the parent process
// boundary, so deferring every tool call back to the user is unnecessary
// noise. Routing all requests through this helper keeps that policy in one
// place.
func AutoApproveOutcome(req acp.RequestPermissionRequest) acp.RequestPermissionOutcome {
	if len(req.Options) == 0 {
		return acp.NewRequestPermissionOutcomeCancelled()
	}

	var (
		allowAlways *acp.PermissionOption
		allowOnce   *acp.PermissionOption
		anyAllow    *acp.PermissionOption
	)
	for i := range req.Options {
		opt := &req.Options[i]
		switch opt.Kind {
		case acp.PermissionOptionKindAllowAlways:
			if allowAlways == nil {
				allowAlways = opt
			}
		case acp.PermissionOptionKindAllowOnce:
			if allowOnce == nil {
				allowOnce = opt
			}
		default:
			if anyAllow == nil && len(opt.Kind) >= 6 && opt.Kind[:6] == "allow_" {
				anyAllow = opt
			}
		}
	}

	switch {
	case allowAlways != nil:
		return acp.NewRequestPermissionOutcomeSelected(allowAlways.OptionId)
	case allowOnce != nil:
		return acp.NewRequestPermissionOutcomeSelected(allowOnce.OptionId)
	case anyAllow != nil:
		return acp.NewRequestPermissionOutcomeSelected(anyAllow.OptionId)
	}

	// No allow-style option found — pick the first option so the turn can
	// proceed rather than stalling on a cancellation.
	return acp.NewRequestPermissionOutcomeSelected(req.Options[0].OptionId)
}
