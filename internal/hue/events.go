package hue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// The bridge reports every state change on GET /eventstream/clip/v2, an SSE
// stream (see sse.go). Each "data:" payload is a JSON array of events, each
// carrying the partial resources that changed:
//
//	[{"creationtime":"2026-09-25T12:00:00Z","id":"<event uuid>","type":"update",
//	  "data":[{"id":"<light id>","type":"light","on":{"on":false}, ...}]}]
//
// Only the changed fields are present, which is why Change uses pointers.

// Change is one resource update from the stream.
type Change struct {
	ResourceID   string
	ResourceType string   // "light", "grouped_light", "scene", "button", ...
	On           *bool    // set when the on/off state was part of the update
	Brightness   *float64 // set when dimming was part of the update
}

// EventHandler receives what happens on the stream. All fields optional.
type EventHandler struct {
	// OnConnect runs once the bridge has accepted the stream request. Watch
	// callers use it to re-read state that may have changed while offline.
	OnConnect func()
	// OnChange runs for every resource in every event.
	OnChange func(Change)
	// OnError runs when a stream attempt fails and Watch is about to retry.
	OnError func(err error, retryIn time.Duration)
}

// StreamEvents opens the stream and blocks, calling h for each change,
// until ctx ends or the connection breaks. It returns nil on ctx
// cancellation and the failure otherwise.
func (c *Client) StreamEvents(ctx context.Context, h EventHandler) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.addr+"/eventstream/clip/v2", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.appKey != "" {
		req.Header.Set(appKeyHeader, c.appKey)
	}

	// The normal client has a whole-request timeout that would cut a
	// never-ending response. Reuse its transport (TLS pinning, debug log)
	// in a client without one; ctx remains the way to stop.
	streaming := &http.Client{Transport: c.http.Transport}
	resp, err := streaming.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: HTTP %d", resp.StatusCode)
	}
	if h.OnConnect != nil {
		h.OnConnect()
	}

	err = readSSE(resp.Body, func(ev sseEvent) {
		debugf(c.log, "events: id=%s %s", ev.ID, ev.Data)
		for _, change := range parseChanges([]byte(ev.Data), c) {
			if h.OnChange != nil {
				h.OnChange(change)
			}
		}
	})
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = errors.New("event stream ended")
	}
	return err
}

// parseChanges decodes one SSE data payload into Changes. Malformed input
// is logged and yields nothing; the stream goes on.
func parseChanges(data []byte, c *Client) []Change {
	var batch []struct {
		Type string `json:"type"` // update, add, delete, error
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			On   *struct {
				On bool `json:"on"`
			} `json:"on"`
			Dimming *struct {
				Brightness float64 `json:"brightness"`
			} `json:"dimming"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		debugf(c.log, "events: ignoring undecodable payload: %v", err)
		return nil
	}
	var changes []Change
	for _, ev := range batch {
		if ev.Type != "update" {
			continue
		}
		for _, r := range ev.Data {
			ch := Change{ResourceID: r.ID, ResourceType: r.Type}
			if r.On != nil {
				on := r.On.On
				ch.On = &on
			}
			if r.Dimming != nil {
				b := r.Dimming.Brightness
				ch.Brightness = &b
			}
			changes = append(changes, ch)
		}
	}
	return changes
}

// Watch keeps a stream open until ctx ends, reconnecting after failures
// with a delay that doubles from one second up to thirty.
func (c *Client) Watch(ctx context.Context, h EventHandler) {
	delay := time.Second
	for {
		err := c.StreamEvents(ctx, h)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("event stream closed")
		}
		if h.OnError != nil {
			h.OnError(err, delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}
