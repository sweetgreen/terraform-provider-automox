package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Automox returns collections in three different envelope shapes and pages them
// three different ways, with no relationship between which endpoint uses which.
// Verified live on 2026-07-30 (org 120547):
//
//	GET /policies?limit=2            -> [ {...}, {...} ]                    bare array
//	GET /orgs?limit=2                -> [ {...} ]                           bare array
//	GET /policystats                 -> [ 26 items ]                        bare array, unpaged
//	GET /data-extracts?limit=2       -> {"results":[...], "size":5}         size is the TOTAL, not the page length
//	GET /wis/search?limit=2          -> {"data":[...], "metadata":{...}}    metadata.total_count / total_pages
//	GET /orgs/{id}/…/action-sets     -> {"data":[...], "metadata":{...}}
//
// Paging is 0-based; page=9999 returns an empty array rather than an error, and a
// final partial page comes back short (26 policies at limit=20 gives 6 on page 1).
//
// Data sources declare their envelope and this file walks every page, so callers
// never see a truncated collection and never have to know which convention an
// endpoint follows.

// Envelope names how an endpoint wraps a collection.
type Envelope int

const (
	// EnvelopeArray is a bare JSON array: /policies, /orgs, /servers, /events,
	// /policystats.
	EnvelopeArray Envelope = iota

	// EnvelopeResults is {"results": [...], "size": N} where N is the total
	// number of records, not the length of this page.
	EnvelopeResults

	// EnvelopeData is {"data": [...], "metadata": {total_count, total_pages,
	// current_page, limit}}.
	EnvelopeData
)

// defaultPageLimit is deliberately below the 500 several endpoints allow. Larger
// pages mean fewer requests, which matters against the <30 req/min ceiling on
// device listing, but an oversized page on a wide model produces very large
// responses. 250 is a middle ground; callers with a known-small collection can
// override it.
const defaultPageLimit = 250

// maxPages bounds a walk so a server that ignores paging cannot loop forever.
// At the default limit this is 250,000 records — far above any real Automox
// collection, and low enough to fail in seconds rather than exhaust memory.
const maxPages = 1000

// ListOptions configures a paginated read.
type ListOptions struct {
	Path     string
	Query    url.Values
	OrgScope OrgScope
	Envelope Envelope

	// Limit is the page size. Zero uses defaultPageLimit.
	Limit int

	// Unpaged marks endpoints that accept no paging parameters and return
	// everything in one response: /policystats, /global/api_keys,
	// /servers/{id}/queues, /accounts/{uuid}/rbac-roles.
	Unpaged bool
}

// metadataEnvelope is the {data, metadata} shape.
type metadataEnvelope struct {
	Data     json.RawMessage `json:"data"`
	Metadata struct {
		TotalCount  int `json:"total_count"`
		TotalPages  int `json:"total_pages"`
		CurrentPage int `json:"current_page"`
		Limit       int `json:"limit"`
	} `json:"metadata"`
}

// resultsEnvelope is the {results, size} shape.
type resultsEnvelope struct {
	Results json.RawMessage `json:"results"`
	Size    int             `json:"size"`
}

// List walks every page of a collection and unmarshals the accumulated records
// into out, which must be a pointer to a slice.
//
// It never returns a partial collection silently: any page failure aborts with
// that error, because a data source returning half a fleet looks identical to a
// fleet that shrank, and Terraform would act on the difference.
func (c *Client) List(ctx context.Context, opts ListOptions, out any) error {
	records, err := c.listRaw(ctx, opts)
	if err != nil {
		return err
	}

	// Re-marshal the accumulated elements so callers get their own typed model
	// without this layer needing to know it.
	combined, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("automox: combining paged results from %s: %w", opts.Path, err)
	}
	if err := json.Unmarshal(combined, out); err != nil {
		return fmt.Errorf("automox: decoding paged results from %s into %T: %w", opts.Path, out, err)
	}
	return nil
}

func (c *Client) listRaw(ctx context.Context, opts ListOptions) ([]json.RawMessage, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}

	var all []json.RawMessage

	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf(
				"automox: %s returned more than %d pages of %d records; refusing to continue "+
					"in case the endpoint is ignoring pagination", opts.Path, maxPages, limit)
		}

		query := url.Values{}
		for k, vs := range opts.Query {
			for _, v := range vs {
				query.Add(k, v)
			}
		}
		if !opts.Unpaged {
			query.Set("limit", strconv.Itoa(limit))
			query.Set("page", strconv.Itoa(page))
		}

		body, err := c.doRaw(ctx, Request{
			Method:   "GET",
			Path:     opts.Path,
			Query:    query,
			OrgScope: opts.OrgScope,
		})
		if err != nil {
			return nil, err
		}

		items, total, err := decodePage(opts.Envelope, opts.Path, body)
		if err != nil {
			return nil, err
		}

		all = append(all, items...)

		if opts.Unpaged {
			return all, nil
		}

		// Termination, in order of reliability:
		//
		// 1. An empty page. Every envelope produces this at the end, and it is
		//    what an out-of-range page returns rather than an error.
		// 2. A short page. A final partial page is shorter than the limit.
		// 3. A declared total reached. Only the metadata and results envelopes
		//    carry one; it guards against an endpoint that keeps echoing a full
		//    page instead of ending.
		if len(items) == 0 || len(items) < limit {
			return all, nil
		}
		if total > 0 && len(all) >= total {
			return all, nil
		}
	}
}

// decodePage extracts the records from one response and, where the envelope
// declares one, the total record count.
func decodePage(envelope Envelope, path string, body []byte) ([]json.RawMessage, int, error) {
	switch envelope {
	case EnvelopeArray:
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, 0, fmt.Errorf(
				"automox: %s was expected to return a JSON array but did not: %w (body: %s)",
				path, err, excerpt(body))
		}
		return items, 0, nil

	case EnvelopeResults:
		var env resultsEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, 0, fmt.Errorf(
				"automox: %s was expected to return {results, size} but did not: %w (body: %s)",
				path, err, excerpt(body))
		}
		items, err := decodeItems(env.Results, path)
		if err != nil {
			return nil, 0, err
		}
		// size is the total record count, verified live: data-extracts reports
		// size=5 whether the page holds 1, 2, or 5 records.
		return items, env.Size, nil

	case EnvelopeData:
		var env metadataEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, 0, fmt.Errorf(
				"automox: %s was expected to return {data, metadata} but did not: %w (body: %s)",
				path, err, excerpt(body))
		}
		items, err := decodeItems(env.Data, path)
		if err != nil {
			return nil, 0, err
		}
		return items, env.Metadata.TotalCount, nil

	default:
		return nil, 0, fmt.Errorf("automox: unknown envelope %d for %s", envelope, path)
	}
}

// decodeItems handles the collection member of an envelope, tolerating null —
// which Automox uses for an empty collection on some endpoints — while still
// rejecting a value that is neither null nor an array.
func decodeItems(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("automox: the collection returned by %s was not an array: %w", path, err)
	}
	return items, nil
}
