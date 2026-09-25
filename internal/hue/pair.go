package hue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Pairing uses the original (v1) endpoint POST /api, which is still the only
// way to obtain an application key. The flow is deliberately physical: the
// bridge answers "link button not pressed" until someone presses the big
// button on the bridge, then accepts the request for about 30 seconds.
//
// Request:  {"devicetype":"hue-cli#fedora","generateclientkey":true}
// Failure:  [{"error":{"type":101,"description":"link button not pressed"}}]
// Success:  [{"success":{"username":"<app key>","clientkey":"<hex>"}}]

// Credentials are the secrets a pairing produces.
type Credentials struct {
	// AppKey goes in the hue-application-key header on every request.
	// The v1 API calls it "username". Treat it like a password.
	AppKey string `json:"app_key"`
	// ClientKey is only used by the Entertainment streaming API. Stored for
	// completeness; nothing in this project uses it yet.
	ClientKey string `json:"client_key,omitempty"`
}

// ErrLinkButtonNotPressed is returned while the bridge waits for the button.
// It is a sentinel error: callers compare with errors.Is.
var ErrLinkButtonNotPressed = errors.New("link button not pressed")

// pairPollInterval is how often Pair retries. A variable so tests can hurry.
var pairPollInterval = time.Second

// CreateAppKey makes one pairing attempt. appName identifies the software
// (max 20 characters), deviceName the machine (max 19); both appear in the
// Hue app's list of connected applications as "appName#deviceName".
func (c *Client) CreateAppKey(ctx context.Context, appName, deviceName string) (Credentials, error) {
	if err := validateDeviceType(appName, deviceName); err != nil {
		return Credentials{}, err
	}
	request := map[string]any{
		"devicetype":        appName + "#" + deviceName,
		"generateclientkey": true,
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api", request)
	if err != nil {
		return Credentials{}, err
	}
	if status != http.StatusOK {
		return Credentials{}, fmt.Errorf("POST /api: HTTP %d: %s", status, trim(body))
	}

	// The v1 API wraps every answer in an array of {success} or {error}
	// objects. Pointers let us tell "absent" from "present but empty".
	var results []struct {
		Success *struct {
			Username  string `json:"username"`
			ClientKey string `json:"clientkey"`
		} `json:"success"`
		Error *struct {
			Type        int    `json:"type"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return Credentials{}, fmt.Errorf("POST /api: bad JSON: %w", err)
	}
	for _, r := range results {
		if r.Success != nil && r.Success.Username != "" {
			return Credentials{AppKey: r.Success.Username, ClientKey: r.Success.ClientKey}, nil
		}
		if r.Error != nil {
			if r.Error.Type == 101 {
				return Credentials{}, ErrLinkButtonNotPressed
			}
			return Credentials{}, fmt.Errorf("bridge error %d: %s", r.Error.Type, r.Error.Description)
		}
	}
	return Credentials{}, fmt.Errorf("POST /api: unexpected response: %s", trim(body))
}

// Pair calls CreateAppKey once per second until the bridge accepts, the
// timeout passes, or ctx is cancelled. onWaiting, if not nil, is called after
// each refused attempt so a CLI can show progress.
func (c *Client) Pair(ctx context.Context, appName, deviceName string, timeout time.Duration, onWaiting func(attempt int)) (Credentials, error) {
	deadline := time.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		creds, err := c.CreateAppKey(ctx, appName, deviceName)
		if err == nil {
			debugf(c.log, "pair: bridge accepted on attempt %d", attempt)
			return creds, nil
		}
		if !errors.Is(err, ErrLinkButtonNotPressed) {
			return Credentials{}, err
		}
		if onWaiting != nil {
			onWaiting(attempt)
		}
		if time.Now().Add(pairPollInterval).After(deadline) {
			return Credentials{}, fmt.Errorf("%w after %s", ErrLinkButtonNotPressed, timeout.Round(time.Second))
		}
		// select waits on several channels at once: here, cancellation or
		// the poll timer, whichever fires first.
		select {
		case <-ctx.Done():
			return Credentials{}, ctx.Err()
		case <-time.After(pairPollInterval):
		}
	}
}

func validateDeviceType(appName, deviceName string) error {
	switch {
	case appName == "" || len(appName) > 20:
		return fmt.Errorf("application name %q must be 1 to 20 characters", appName)
	case deviceName == "" || len(deviceName) > 19:
		return fmt.Errorf("device name %q must be 1 to 19 characters", deviceName)
	case strings.ContainsAny(appName+deviceName, "#"):
		return errors.New("names must not contain '#'")
	}
	return nil
}
