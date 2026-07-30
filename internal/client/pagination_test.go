package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type item struct {
	ID int `json:"id"`
}

// pagedServer serves `total` records in pages, using the given envelope. It also
// records every page requested, so tests can assert the walk itself.
func pagedServer(t *testing.T, envelope Envelope, total int) (*Client, *[]int) {
	t.Helper()
	var pagesSeen []int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		pagesSeen = append(pagesSeen, page)

		start := page * limit
		end := start + limit
		if start > total {
			start = total
		}
		if end > total {
			end = total
		}

		items := make([]item, 0, end-start)
		for i := start; i < end; i++ {
			items = append(items, item{ID: i})
		}

		body, _ := json.Marshal(items)
		switch envelope {
		case EnvelopeArray:
			w.Write(body)
		case EnvelopeResults:
			fmt.Fprintf(w, `{"results":%s,"size":%d}`, body, total)
		case EnvelopeData:
			pages := (total + limit - 1) / limit
			fmt.Fprintf(w,
				`{"data":%s,"metadata":{"total_count":%d,"total_pages":%d,"current_page":%d,"limit":%d}}`,
				body, total, pages, page, limit)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, &pagesSeen
}

// The core promise: a caller gets the whole collection regardless of envelope.
// A truncated fleet listing is indistinguishable from a fleet that shrank, and
// Terraform would act on the difference.
func TestUnitList_WalksEveryPageForEachEnvelope(t *testing.T) {
	envelopes := map[string]Envelope{
		"array":   EnvelopeArray,
		"results": EnvelopeResults,
		"data":    EnvelopeData,
	}

	for name, envelope := range envelopes {
		for _, total := range []int{0, 1, 9, 10, 11, 25} {
			t.Run(fmt.Sprintf("%s/%d", name, total), func(t *testing.T) {
				c, pages := pagedServer(t, envelope, total)

				var got []item
				err := c.List(context.Background(), ListOptions{
					Path: "/things", OrgScope: OrgScopeQuery, Envelope: envelope, Limit: 10,
				}, &got)
				if err != nil {
					t.Fatalf("List: %v", err)
				}

				if len(got) != total {
					t.Fatalf("got %d records, want %d (pages requested: %v)", len(got), total, *pages)
				}
				for i, rec := range got {
					if rec.ID != i {
						t.Fatalf("record %d has id %d; ordering or dedup is wrong", i, rec.ID)
					}
				}
			})
		}
	}
}

// A full final page must be followed by one more request to discover the end;
// stopping early would silently drop the tail.
func TestUnitList_HandlesExactPageBoundary(t *testing.T) {
	c, pages := pagedServer(t, EnvelopeArray, 20)

	var got []item
	if err := c.List(context.Background(), ListOptions{
		Path: "/things", OrgScope: OrgScopeQuery, Envelope: EnvelopeArray, Limit: 10,
	}, &got); err != nil {
		t.Fatal(err)
	}

	if len(got) != 20 {
		t.Errorf("got %d records, want 20", len(got))
	}
	// Pages 0 and 1 are full, so page 2 must be probed and come back empty.
	if len(*pages) != 3 {
		t.Errorf("requested pages %v, expected three (two full plus an empty probe)", *pages)
	}
}

// A declared total lets the walk stop without the extra probe.
func TestUnitList_StopsOnDeclaredTotalWithoutExtraRequest(t *testing.T) {
	for _, envelope := range []Envelope{EnvelopeResults, EnvelopeData} {
		c, pages := pagedServer(t, envelope, 20)

		var got []item
		if err := c.List(context.Background(), ListOptions{
			Path: "/things", OrgScope: OrgScopeQuery, Envelope: envelope, Limit: 10,
		}, &got); err != nil {
			t.Fatal(err)
		}

		if len(got) != 20 {
			t.Errorf("envelope %v: got %d records, want 20", envelope, len(got))
		}
		if len(*pages) != 2 {
			t.Errorf("envelope %v: requested pages %v, expected two — the declared total should "+
				"avoid the empty probe", envelope, *pages)
		}
	}
}

// Endpoints such as /policystats accept no paging parameters at all. Sending
// them anyway risks an endpoint interpreting them unexpectedly.
func TestUnitList_UnpagedSendsNoPagingParametersAndFetchesOnce(t *testing.T) {
	var requests int
	var sawPaging bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		if q.Has("page") || q.Has("limit") {
			sawPaging = true
		}
		w.Write([]byte(`[{"id":0},{"id":1},{"id":2}]`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var got []item
	if err := c.List(context.Background(), ListOptions{
		Path: "/policystats", OrgScope: OrgScopeQuery, Envelope: EnvelopeArray, Unpaged: true,
	}, &got); err != nil {
		t.Fatal(err)
	}

	if requests != 1 {
		t.Errorf("made %d requests, want exactly 1 for an unpaged endpoint", requests)
	}
	if sawPaging {
		t.Error("paging parameters were sent to an endpoint that does not accept them")
	}
	if len(got) != 3 {
		t.Errorf("got %d records, want 3", len(got))
	}
}

// A page failure must abort. Returning what was collected so far would look
// exactly like a collection that shrank.
func TestUnitList_FailsLoudlyMidWalkRatherThanTruncating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "0" {
			w.Write([]byte(`[{"id":0},{"id":1}]`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"errors":["upstream exploded"]}`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var got []item
	err := c.List(context.Background(), ListOptions{
		Path: "/things", OrgScope: OrgScopeQuery, Envelope: EnvelopeArray, Limit: 2,
	}, &got)

	if err == nil {
		t.Fatal("a mid-walk failure must abort, not return a partial collection")
	}
	if len(got) != 0 {
		t.Errorf("partial results were written to the caller: %v", got)
	}
}

// A server that ignores paging and always returns a full page would otherwise
// loop until memory ran out.
func TestUnitList_BoundsRunawayPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":1},{"id":2}]`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var got []item
	err := c.List(context.Background(), ListOptions{
		Path: "/things", OrgScope: OrgScopeQuery, Envelope: EnvelopeArray, Limit: 2,
	}, &got)

	if err == nil {
		t.Fatal("expected the runaway guard to trigger")
	}
	if !strings.Contains(err.Error(), "ignoring pagination") {
		t.Errorf("error %q should explain why it stopped", err)
	}
}

// Automox uses null for an empty collection on some endpoints.
func TestUnitList_TreatsNullCollectionAsEmpty(t *testing.T) {
	for _, body := range []string{`{"data":null,"metadata":{"total_count":0}}`, `{"results":null,"size":0}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))

		envelope := EnvelopeData
		if strings.Contains(body, "results") {
			envelope = EnvelopeResults
		}

		c, _ := New(Options{
			BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
			HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
		})

		var got []item
		err := c.List(context.Background(), ListOptions{
			Path: "/things", OrgScope: OrgScopeQuery, Envelope: envelope, Limit: 10,
		}, &got)
		srv.Close()

		if err != nil {
			t.Errorf("null collection should decode as empty, got: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d records, want 0", len(got))
		}
	}
}

// A wrong envelope is a programming error in a data source, and must name what
// was expected rather than surfacing an opaque decode failure.
func TestUnitList_MismatchedEnvelopeIsDiagnosable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"id":1}],"size":1}`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var got []item
	err := c.List(context.Background(), ListOptions{
		Path: "/data-extracts", OrgScope: OrgScopeQuery, Envelope: EnvelopeArray, Limit: 10,
	}, &got)

	if err == nil {
		t.Fatal("expected an error for a mismatched envelope")
	}
	for _, want := range []string{"/data-extracts", "JSON array"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// Filter parameters must survive onto every page, or later pages silently widen
// the result set.
func TestUnitList_PreservesCallerQueryAcrossPages(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query().Get("status:in"))
		page := r.URL.Query().Get("page")
		if page == "0" {
			w.Write([]byte(`[{"id":0},{"id":1}]`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var got []item
	if err := c.List(context.Background(), ListOptions{
		Path:     "/action-sets",
		OrgScope: OrgScopeQuery,
		Envelope: EnvelopeArray,
		Limit:    2,
		Query:    map[string][]string{"status:in": {"ready"}},
	}, &got); err != nil {
		t.Fatal(err)
	}

	if len(seen) < 2 {
		t.Fatalf("expected at least two pages, saw %d", len(seen))
	}
	for i, v := range seen {
		if v != "ready" {
			t.Errorf("page %d lost the filter: status:in=%q", i, v)
		}
	}
}
