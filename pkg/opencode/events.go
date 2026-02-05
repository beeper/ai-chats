package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamEvents connects to the OpenCode /event SSE endpoint.
// It returns a channel of events and a channel for errors.
func (c *Client) StreamEvents(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event, 8)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		req, err := c.newRequest(ctx, http.MethodGet, "/event", nil)
		if err != nil {
			errs <- err
			return
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := c.http.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			errs <- fmt.Errorf("opencode event stream error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

		var dataLines []string
		flush := func() {
			if len(dataLines) == 0 {
				return
			}
			payload := strings.Join(dataLines, "\n")
			dataLines = nil

			var evt Event
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				errs <- err
				return
			}
			events <- evt
		}

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			if line == "" {
				flush()
				continue
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			errs <- err
		}
	}()

	return events, errs
}
