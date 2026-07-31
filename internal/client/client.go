// Package client is the HTTP transport for the Automox Console API.
//
// It centralises the behaviour every resource and data source depends on: bearer
// authentication, organization scoping, the five error envelopes the service uses,
// retry on rate limiting, and the inconsistent ways absence is reported.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the only server the API advertises. Exposed as provider
// configuration anyway, because Automox operates regional consoles the published
// document does not model.
const DefaultBaseURL = "https://console.automox.com/api"

const defaultTimeout = 60 * time.Second

// Client talks to the Automox Console API on behalf of the provider.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	httpClient *http.Client

	// orgID is the default organization for endpoints scoped by the `?o=` query
	// parameter, which is most of them.
	orgID int64

	// orgs resolves the integer id ↔ UUID pairing that different endpoint
	// families require. Populated lazily; see organization.go.
	orgs *orgCache

	retry RetryPolicy
}

// Options configures a Client.
type Options struct {
	BaseURL        string
	APIKey         string
	OrganizationID int64
	HTTPClient     *http.Client
	Retry          *RetryPolicy
}

// New builds a Client. It validates configuration eagerly so a misconfigured
// provider fails at configure time rather than on the first resource operation.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("automox: api_key is required")
	}

	rawURL := opts.BaseURL
	if strings.TrimSpace(rawURL) == "" {
		rawURL = DefaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("automox: base_url %q is not a valid URL: %w", rawURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("automox: base_url %q must include a scheme and host", rawURL)
	}

	// Logging is layered onto whatever transport the caller supplied, rather than
	// only onto the default one, so a custom client (a test server, a proxy for
	// capture) is still observable.
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	} else {
		clone := *httpClient
		httpClient = &clone
	}
	httpClient.Transport = newLoggingTransport(httpClient.Transport, opts.APIKey)

	retry := DefaultRetryPolicy()
	if opts.Retry != nil {
		retry = *opts.Retry
	}

	return &Client{
		baseURL:    parsed,
		apiKey:     opts.APIKey,
		httpClient: httpClient,
		orgID:      opts.OrganizationID,
		orgs:       newOrgCache(),
		retry:      retry,
	}, nil
}

// OrganizationID returns the configured default organization.
func (c *Client) OrganizationID() int64 { return c.orgID }

// Request describes one API call.
type Request struct {
	Method string
	Path   string

	// Query parameters. Automox uses several parameter names that are not valid
	// Go identifiers and would be mangled by struct-tag encoding — `type:equals`,
	// `status:in`, `policy_id[]` — so callers supply them literally.
	Query url.Values

	// Body is marshalled to JSON when non-nil.
	Body any

	// Upload sends a multipart/form-data body instead of JSON. Automox accepts
	// worklet attachments only this way -- a JSON body with the same content is
	// rejected with "The file field is required."
	//
	// Body and Upload are mutually exclusive; setting both is a programming error
	// and fails the request rather than silently preferring one.
	Upload *Upload

	// OrgScope selects how this endpoint expects the organization to be supplied.
	OrgScope OrgScope
}

// OrgScope describes how an endpoint is scoped to an organization. Automox uses
// three mutually incompatible conventions for the same entity, so each call site
// declares which one applies rather than the transport guessing from the path.
type OrgScope int

const (
	// OrgScopeNone is for endpoints that take no organization: /orgs,
	// /accounts/*, /global/api_keys, /wis/search.
	OrgScopeNone OrgScope = iota

	// OrgScopeQuery appends `?o=<int id>`, required by most endpoints.
	OrgScopeQuery
)

// Do performs a request and unmarshals a JSON response body into out.
//
// out may be nil for endpoints that return no content — PUT and DELETE on
// policies, server groups, and devices all return 204.
func (c *Client) Do(ctx context.Context, req Request, out any) error {
	body, err := c.doRaw(ctx, req)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("automox api: %s %s returned a body that could not be decoded: %w (body: %s)",
			req.Method, req.Path, err, excerpt(body))
	}
	return nil
}

// doRaw performs a request and returns the raw response body.
func (c *Client) doRaw(ctx context.Context, req Request) ([]byte, error) {
	endpoint, err := c.resolve(req)
	if err != nil {
		return nil, err
	}

	if req.Body != nil && req.Upload != nil {
		return nil, fmt.Errorf(
			"automox: %s %s sets both Body and Upload; this is a bug in the provider",
			req.Method, req.Path)
	}

	var payload []byte
	contentType := ""

	switch {
	case req.Upload != nil:
		payload, contentType, err = encodeMultipart(*req.Upload)
		if err != nil {
			return nil, fmt.Errorf("automox: encoding upload for %s %s: %w",
				req.Method, req.Path, err)
		}
	case req.Body != nil:
		payload, err = json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("automox: encoding request body for %s %s: %w",
				req.Method, req.Path, err)
		}
		contentType = "application/json"
	}

	return c.retry.Do(ctx, func() ([]byte, error) {
		return c.attempt(ctx, req.Method, endpoint, payload, contentType)
	})
}

// Upload is a single file sent as multipart/form-data.
type Upload struct {
	// FieldName is the form field. Automox requires "file".
	FieldName string
	Filename  string
	Content   []byte
}

// encodeMultipart builds the body in full rather than streaming it.
//
// The retry layer replays the payload on a 429 or 503, which a streaming reader
// could not do -- it would already be drained, and the retry would upload an
// empty file while reporting success.
func encodeMultipart(up Upload) ([]byte, string, error) {
	field := up.FieldName
	if field == "" {
		field = "file"
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile(field, up.Filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(up.Content); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return buf.Bytes(), w.FormDataContentType(), nil
}

func (c *Client) attempt(ctx context.Context, method, endpoint string, payload []byte, contentType string) ([]byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("automox: building %s %s: %w", method, redactURL(endpoint), err)
	}

	// The API key travels only here. It is never placed in a query parameter,
	// which would leak it into access logs and error messages.
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "application/json")
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A transport failure is never absence and never an empty result.
		return nil, fmt.Errorf("automox: %s %s failed: %w", method, redactURL(endpoint), err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("automox: reading response from %s %s (status %d): %w",
			method, redactURL(endpoint), resp.StatusCode, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := decodeError(resp.StatusCode, method, redactURL(endpoint), respBody)
		apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, apiErr
	}

	return respBody, nil
}

// resolve builds the absolute URL, applying organization scoping.
func (c *Client) resolve(req Request) (string, error) {
	path := strings.TrimPrefix(req.Path, "/")

	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/" + path

	query := url.Values{}
	for k, vs := range req.Query {
		for _, v := range vs {
			query.Add(k, v)
		}
	}

	if req.OrgScope == OrgScopeQuery && query.Get("o") == "" {
		if c.orgID == 0 {
			return "", fmt.Errorf(
				"automox: %s %s requires an organization, but organization_id is not configured "+
					"and none was supplied", req.Method, req.Path)
		}
		query.Set("o", strconv.FormatInt(c.orgID, 10))
	}

	u.RawQuery = query.Encode()
	return u.String(), nil
}

// redactURL is defence in depth. Automox authenticates by header, so a key should
// never reach a URL — but if a future call site puts one in a query parameter, it
// must not travel into a Terraform diagnostic.
func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := parsed.Query()
	for _, sensitive := range []string{"api_key", "apikey", "key", "token", "access_key"} {
		if q.Has(sensitive) {
			q.Set(sensitive, "REDACTED")
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
