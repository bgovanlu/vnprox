// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"fmt"
	"time"
)

// DefaultWaitInterval, DefaultWaitMaxInterval, and DefaultWaitMultiplier
// are WaitOptions' zero-value defaults: poll every 200ms, backing off by
// 1.5x up to a 5s ceiling.
const (
	DefaultWaitInterval    = 200 * time.Millisecond
	DefaultWaitMaxInterval = 5 * time.Second
	DefaultWaitMultiplier  = 1.5
)

// WaitOptions configures WaitTask's polling backoff and overall timeout.
type WaitOptions struct {
	// Interval is the first poll delay. Zero means DefaultWaitInterval.
	Interval time.Duration
	// MaxInterval caps the backoff. Zero means DefaultWaitMaxInterval.
	MaxInterval time.Duration
	// Multiplier grows Interval after each poll. Zero (or <1) means
	// DefaultWaitMultiplier.
	Multiplier float64
	// Timeout bounds the whole wait, independent of ctx's own deadline
	// (whichever is sooner wins). Zero means "rely on ctx alone".
	Timeout time.Duration
}

func (o WaitOptions) withDefaults() WaitOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultWaitInterval
	}
	if o.MaxInterval <= 0 {
		o.MaxInterval = DefaultWaitMaxInterval
	}
	if o.Multiplier < 1 {
		o.Multiplier = DefaultWaitMultiplier
	}
	return o
}

// GetTask calls GET /nodes/{node}/tasks/{upid}/status once.
func (c *Client) GetTask(ctx context.Context, node, upid string) (*TaskStatus, error) {
	var out TaskStatus
	path := fmt.Sprintf("/nodes/%s/tasks/%s/status", node, upid)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTaskLog calls GET /nodes/{node}/tasks/{upid}/log.
func (c *Client) GetTaskLog(ctx context.Context, node, upid string) ([]TaskLogLine, error) {
	var out []TaskLogLine
	path := fmt.Sprintf("/nodes/%s/tasks/%s/log", node, upid)
	if err := c.do(ctx, "GET", path, requestParams{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WaitTask polls GET /nodes/{node}/tasks/{upid}/status with exponential
// backoff (per opts) until the task reaches a terminal status, opts.Timeout
// elapses, or ctx is done — whichever comes first.
//
// On success (task stopped with a non-failed exit status) it returns the
// final TaskStatus and a nil error. On a failed exit status it returns the
// final TaskStatus alongside *ErrPVETaskFailed. On timeout/cancellation it
// returns a nil TaskStatus and *ErrPVETaskTimeout.
func (c *Client) WaitTask(ctx context.Context, node, upid string, opts WaitOptions) (*TaskStatus, error) {
	opts = opts.withDefaults()

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	interval := opts.Interval
	for {
		t, err := c.GetTask(ctx, node, upid)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, &ErrPVETaskTimeout{UPID: upid, Err: ctxErr}
			}
			return nil, err
		}

		if !t.Running() {
			if t.Failed() {
				return t, &ErrPVETaskFailed{UPID: upid, ExitStatus: t.ExitStatus}
			}
			return t, nil
		}

		select {
		case <-ctx.Done():
			return nil, &ErrPVETaskTimeout{UPID: upid, Err: ctx.Err()}
		case <-time.After(interval):
		}

		interval = time.Duration(float64(interval) * opts.Multiplier)
		if interval > opts.MaxInterval {
			interval = opts.MaxInterval
		}
	}
}
