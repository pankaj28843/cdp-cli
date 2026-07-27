package chatgpt

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/admission"
	"github.com/pankaj28843/cdp-cli/internal/webagent"
)

const chatGPTThrottleProvider = "chatgpt-rate"

type heldChatGPTThrottle struct {
	lease *admission.Lease
	once  sync.Once
	err   error
}

func acquireChatGPTThrottle(
	ctx context.Context,
	gate *admission.Gate,
) (*admission.Lease, *readFailure) {
	if gate == nil {
		return nil, internalReadFailure(
			"ChatGPT shared provider throttle is unavailable",
		)
	}
	lease, err := gate.Acquire(ctx, admission.Request{
		Provider:  chatGPTThrottleProvider,
		Operation: "transport",
		RunID:     webagent.NewRunID(),
	})
	if err == nil {
		return lease, nil
	}
	var blocked *admission.BlockedError
	if errors.As(err, &blocked) {
		failure := &readFailure{
			code:     "chatgpt_throttle_blocked",
			errClass: "rate_limit",
			message:  blocked.Error(),
			retryAt:  blocked.RetryAt,
		}
		if blocked.ResolutionNeeded {
			failure.errClass = "admission"
		}
		return nil, failure
	}
	return nil, internalReadFailure(
		"ChatGPT shared provider throttle state is unavailable",
	)
}

func releaseChatGPTThrottle(
	lease *admission.Lease,
	failure *readFailure,
) error {
	if lease == nil {
		return nil
	}
	outcome := admission.OutcomeCompleted
	var cooldown time.Time
	if failure != nil {
		outcome = admission.OutcomeFailed
		if failure.code == "chatgpt_rate_limited" {
			outcome = admission.OutcomeRateLimited
			cooldown = failure.retryAt
		}
	}
	return lease.Release(admission.Release{
		Outcome:       outcome,
		CooldownUntil: cooldown,
	})
}

func holdChatGPTThrottle(
	ctx context.Context,
	gate *admission.Gate,
) (*heldChatGPTThrottle, *readFailure) {
	lease, failure := acquireChatGPTThrottle(ctx, gate)
	if failure != nil {
		return nil, failure
	}
	return &heldChatGPTThrottle{lease: lease}, nil
}

func (held *heldChatGPTThrottle) Release(failure *readFailure) error {
	if held == nil {
		return nil
	}
	held.once.Do(func() {
		held.err = releaseChatGPTThrottle(held.lease, failure)
	})
	return held.err
}

func persistChatGPTThrottleCooldown(
	ctx context.Context,
	gate *admission.Gate,
	cooldown time.Time,
) error {
	if gate == nil {
		return errors.New("ChatGPT shared provider throttle is unavailable")
	}
	if cooldown.IsZero() {
		cooldown = time.Now().UTC().Add(DefaultAdmissionSpacing)
	}
	return gate.ExtendCooldown(ctx, admission.Request{
		Provider:  chatGPTThrottleProvider,
		Operation: "transport",
		RunID:     webagent.NewRunID(),
	}, cooldown)
}
