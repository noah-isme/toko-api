package resilience

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// HTTPClient wraps an http.Client with retry, timeout and circuit-breaker logic.
type HTTPClient struct {
	Client      *http.Client
	Breaker     *Breaker
	BaseBackoff time.Duration
	MaxAttempts int
	Jitter      float64
	Timeout     time.Duration
	Fallback    func(context.Context, *http.Request, error) (*http.Response, error)
}

// Do executes the request applying retry semantics. The provided request body is
// buffered automatically to support retries. When the breaker is open
// ErrOpenCircuit is returned unless a fallback is configured.
func (cl HTTPClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if cl.Client == nil {
		return nil, errors.New("resilience: http client not configured")
	}
	breaker := cl.Breaker
	if breaker == nil {
		// default to closed breaker that never trips
		breaker = NewBreaker(1, 1, time.Second)
	}
	maxAttempts := cl.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	baseBackoff := cl.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = 100 * time.Millisecond
	}

	originalBody, err := ensureReplayableBody(req)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !breaker.Allow() {
			lastErr = ErrOpenCircuit
			break
		}
		attemptReq, err := cloneRequestWithContext(ctx, req, originalBody)
		if err != nil {
			breaker.Report(false)
			return nil, err
		}
		resp, err := cl.doOnce(ctx, attemptReq)
		if err == nil && resp.StatusCode < 500 {
			breaker.Report(true)
			return resp, nil
		}
		if err == nil {
			lastErr = errors.New(resp.Status)
		} else {
			lastErr = err
		}
		breaker.Report(false)
		if attempt == maxAttempts {
			break
		}
		sleepFor := Backoff(baseBackoff, attempt, cl.Jitter)
		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if cl.Fallback != nil {
		return cl.Fallback(ctx, req, lastErr)
	}
	return nil, lastErr
}

func (cl HTTPClient) doOnce(ctx context.Context, req *http.Request) (*http.Response, error) {
	timeout := cl.Timeout
	if timeout <= 0 {
		timeout = cl.Client.Timeout
	}
	var callCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		callCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	req = req.WithContext(callCtx)
	return cl.Client.Do(req)
}

func ensureReplayableBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = body.Close() }()
		data, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(data))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
		return data, nil
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return data, nil
}

func cloneRequestWithContext(ctx context.Context, req *http.Request, body []byte) (*http.Request, error) {
	clone := req.Clone(ctx)
	if body != nil {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return clone, nil
}
