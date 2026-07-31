package client

import (
	"context"
	"fmt"
	"time"
)

// Automox documents exactly one rate limit, on the endpoint the provider reads
// most heavily:
//
//	GET /servers: "This endpoint is rate limited to <30 requests per minute.
//	               Rate limited clients will receive a 429 for 1 minute."
//
// No X-RateLimit-* headers are modelled anywhere and no operation declares a 429
// response, so a client cannot know its remaining budget. The only safe posture is
// to back off generously when told to, and to honour Retry-After when present.

// RetryPolicy controls backoff for transient failures.
type RetryPolicy struct {
	// MaxAttempts includes the initial try. 1 disables retrying.
	MaxAttempts int

	// InitialBackoff is the first delay; it doubles each attempt up to MaxBackoff.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration

	// RateLimitBackoff is used for 429 when the server sends no Retry-After.
	// Automox states the penalty lasts a full minute, so retrying sooner just
	// burns another request against the same limit.
	RateLimitBackoff time.Duration

	// MaxRetryAfter caps how long a server-supplied Retry-After is honoured.
	//
	// MaxBackoff bounds only the local exponential backoff; Retry-After comes
	// from the server and bypassed it entirely, so a header of 86400 would have
	// parked a Terraform apply for a day with no output. Terraform gives no
	// indication it is sleeping, so that is indistinguishable from a hang.
	//
	// Beyond this ceiling the request is not retried at all. Waiting two minutes
	// when the server asked for a day would just fail again, and reporting the
	// rate limit lets the practitioner decide when to re-run. Zero disables the
	// ceiling.
	MaxRetryAfter time.Duration

	// sleep is injectable so tests do not spend real time.
	sleep func(context.Context, time.Duration) error
}

// DefaultRetryPolicy is tuned to the one documented limit.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:      4,
		InitialBackoff:   time.Second,
		MaxBackoff:       30 * time.Second,
		RateLimitBackoff: 60 * time.Second,
		// Above the 60s rate-limit penalty, so a legitimate Automox 429 is still
		// waited out, but far below anything that would look like a hung apply.
		MaxRetryAfter: 5 * time.Minute,
	}
}

// Do runs attempt, retrying while the error is retryable and attempts remain.
//
// Only 429, 409, and 503 are retried; see IsRetryable. A 403 is never retried,
// because no amount of waiting grants permission, and retrying would triple the
// latency of the most common failure under a read-only credential.
func (p RetryPolicy) Do(ctx context.Context, attempt func() ([]byte, error)) ([]byte, error) {
	maxAttempts := p.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	backoff := p.InitialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}

	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			delay := p.delayFor(lastErr, backoff)

			// A server asking for longer than the ceiling is surfaced rather than
			// slept through. This only triggers on a server-supplied Retry-After;
			// local backoff is already bounded by MaxBackoff.
			if p.MaxRetryAfter > 0 && delay > p.MaxRetryAfter {
				return nil, fmt.Errorf(
					"%w (server asked to wait %s, longer than the %s this provider will "+
						"wait; not retried)", lastErr, delay.Round(time.Second), p.MaxRetryAfter)
			}

			if err := p.doSleep(ctx, delay); err != nil {
				// Context cancelled mid-backoff. Report the original failure with
				// the reason we stopped, rather than a bare context error that
				// hides what actually went wrong.
				return nil, fmt.Errorf("%w (retry abandoned: %v)", lastErr, err)
			}
			if backoff < p.MaxBackoff {
				backoff *= 2
				if p.MaxBackoff > 0 && backoff > p.MaxBackoff {
					backoff = p.MaxBackoff
				}
			}
		}

		body, err := attempt()
		if err == nil {
			return body, nil
		}
		lastErr = err

		if !IsRetryable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%w (giving up after %d attempts)", lastErr, maxAttempts)
}

// delayFor prefers the server's own guidance over local backoff.
func (p RetryPolicy) delayFor(err error, backoff time.Duration) time.Duration {
	if apiErr, ok := err.(*APIError); ok {
		if apiErr.RetryAfter > 0 {
			return apiErr.RetryAfter
		}
		if apiErr.StatusCode == 429 && p.RateLimitBackoff > 0 {
			return p.RateLimitBackoff
		}
	}
	return backoff
}

func (p RetryPolicy) doSleep(ctx context.Context, d time.Duration) error {
	if p.sleep != nil {
		return p.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
