package hue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Everything from here on uses the CLIP v2 API (CLIP is Signify's name for
// the bridge's REST interface). Only two calls in this package still use the
// original v1 API, because v2 never got equivalents: the unauthenticated
// /api/0/config probe and POST /api for creating a key (see pair.go).
//
// v2 conventions this file encodes once so the resource files stay short:
//
//   - Paths are /clip/v2/resource/<type>[/<id>].
//   - Every response is an envelope {"errors":[{"description":...}],"data":[...]}.
//     Errors can arrive with HTTP 200, 207 (partial success) or 4xx alike, so
//     the envelope is checked regardless of the status code.
//   - Resources point at each other with {"rid": "<uuid>", "rtype": "<type>"}.
//   - Writes are PUT with a partial object containing only the fields to
//     change, e.g. {"on":{"on":true}}. Reads return the full object.

const v2Prefix = "/clip/v2/resource/"

// Ref is a v2 resource reference, used for owner/children/services links.
type Ref struct {
	RID   string `json:"rid"`
	RType string `json:"rtype"`
}

// v2Envelope is the outer shape of every v2 response. Data stays raw so the
// caller decodes it into the right slice type.
type v2Envelope struct {
	Errors []struct {
		Description string `json:"description"`
	} `json:"errors"`
	Data json.RawMessage `json:"data"`
}

// v2 performs one v2 request. path is relative to v2Prefix, e.g. "light" or
// "light/<id>". body may be nil. out, if not nil, receives the decoded data
// array and must be a pointer to a slice (the API always returns a list).
func (c *Client) v2(ctx context.Context, method, path string, body, out any) error {
	status, raw, err := c.do(ctx, method, v2Prefix+path, body)
	if err != nil {
		return err
	}

	var env v2Envelope
	if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
		// Not even an envelope: report the status and a snippet.
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, trim(raw))
	}
	if len(env.Errors) > 0 {
		descriptions := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			descriptions = append(descriptions, e.Description)
		}
		return fmt.Errorf("%s %s: bridge said: %s", method, path, strings.Join(descriptions, "; "))
	}
	switch status {
	case http.StatusOK, http.StatusMultiStatus:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s %s: application key rejected (HTTP %d); run `hue auth --force`", method, path, status)
	default:
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, trim(raw))
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("%s %s: cannot decode data: %w", method, path, err)
		}
	}
	return nil
}

// getOne fetches a single resource. The API wraps even one object in a list;
// this helper unwraps it and turns an empty list into a clear error.
func getOne[T any](ctx context.Context, c *Client, path string) (T, error) {
	// [T any] makes this a generic function: T is filled in per call site
	// (Light, GroupedLight...). It saves writing the same three lines for
	// every resource type.
	var items []T
	var zero T
	if err := c.v2(ctx, http.MethodGet, path, nil, &items); err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, fmt.Errorf("GET %s: not found", path)
	}
	return items[0], nil
}
