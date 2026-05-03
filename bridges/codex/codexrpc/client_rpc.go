package codexrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

func (c *Client) Initialize(ctx context.Context, info ClientInfo, experimental bool) (string, error) {
	return c.InitializeWithOptions(ctx, info, InitializeOptions{ExperimentalAPI: experimental})
}

func (c *Client) InitializeWithOptions(ctx context.Context, info ClientInfo, opts InitializeOptions) (string, error) {
	params := initializeParamsWire{
		ClientInfo: info,
	}
	if opts.ExperimentalAPI || len(opts.OptOutNotificationMethods) > 0 {
		params.Capabilities = &InitializeCapabilities{
			ExperimentalAPI: opts.ExperimentalAPI,
		}
		if len(opts.OptOutNotificationMethods) > 0 {
			params.Capabilities.OptOutNotificationMethods = slices.Clone(opts.OptOutNotificationMethods)
		}
	}
	var result struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return "", err
	}
	// Followed by initialized notification.
	if err := c.Notify(ctx, "initialized", map[string]any{}); err != nil {
		return "", err
	}
	return result.UserAgent, nil
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	msg := map[string]any{
		"method": method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSONL(ctx, msg)
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	const maxRetries = 5
	for attempt := 0; ; attempt++ {
		idNum := c.nextID.Add(1)
		idRaw, _ := json.Marshal(idNum)
		ch := make(chan Response, 1)
		c.pending.Store(idKey(idRaw), ch)

		req := map[string]any{
			"id":     idNum,
			"method": method,
		}
		if params != nil {
			req["params"] = params
		}
		if err := c.writeJSONL(ctx, req); err != nil {
			c.pending.Delete(idKey(idRaw))
			return err
		}

		var resp Response
		select {
		case resp = <-ch:
		case <-ctx.Done():
			c.pending.Delete(idKey(idRaw))
			return ctx.Err()
		}
		c.pending.Delete(idKey(idRaw))
		if resp.Error != nil {
			if c.wsConn != nil && shouldRetryServerOverloaded(resp.Error) && attempt < maxRetries {
				if err := waitRetryBackoff(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return errors.New("missing rpc result")
		}
		return json.Unmarshal(resp.Result, out)
	}
}
