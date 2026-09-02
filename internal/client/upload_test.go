package client

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUnitUpload_SendsMultipart covers the encoding Automox requires for worklet
// attachments. A JSON body with the same content is rejected with "The file
// field is required", so the content type and form field are contractual.
func TestUnitUpload_SendsMultipart(t *testing.T) {
	var gotContentType, gotField, gotFilename, gotContent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")

		_, params, err := mime.ParseMediaType(gotContentType)
		if err != nil {
			t.Errorf("parsing Content-Type %q: %v", gotContentType, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Errorf("reading the first part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotField = part.FormName()
		gotFilename = part.FileName()
		body, _ := io.ReadAll(part)
		gotContent = string(body)

		_, _ = w.Write([]byte(`{"uuid":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out struct {
		UUID string `json:"uuid"`
	}
	if err := c.Do(context.Background(), Request{
		Method:   "POST",
		Path:     "/policies/1/files",
		OrgScope: OrgScopeQuery,
		Upload:   &Upload{Filename: "install.ps1", Content: []byte("Write-Output 'hi'")},
	}, &out); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(gotContentType, "multipart/form-data;") {
		t.Errorf("Content-Type is %q, want multipart/form-data", gotContentType)
	}
	// Automox names this field "file" and rejects anything else.
	if gotField != "file" {
		t.Errorf("form field is %q, want %q", gotField, "file")
	}
	if gotFilename != "install.ps1" {
		t.Errorf("filename is %q, want install.ps1", gotFilename)
	}
	if gotContent != "Write-Output 'hi'" {
		t.Errorf("content is %q", gotContent)
	}
	if out.UUID != "abc" {
		t.Errorf("response not decoded: %+v", out)
	}
}

// TestUnitUpload_RetryReplaysTheBody guards the reason the payload is buffered
// rather than streamed: a retried upload must send the file again, not an empty
// body that the API would accept as a zero-length file.
func TestUnitUpload_RetryReplaysTheBody(t *testing.T) {
	var sizes []int
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			sizes = append(sizes, -1)
		} else {
			body, _ := io.ReadAll(part)
			sizes = append(sizes, len(body))
		}

		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"uuid":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(),
		Retry: &RetryPolicy{
			MaxAttempts:      3,
			InitialBackoff:   time.Millisecond,
			MaxBackoff:       time.Millisecond,
			RateLimitBackoff: time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Do(context.Background(), Request{
		Method:   "POST",
		Path:     "/policies/1/files",
		OrgScope: OrgScopeQuery,
		Upload:   &Upload{Filename: "f.txt", Content: []byte("0123456789")},
	}, nil); err != nil {
		t.Fatal(err)
	}

	if len(sizes) != 2 {
		t.Fatalf("server saw %d attempts, want 2", len(sizes))
	}
	for i, n := range sizes {
		if n != 10 {
			t.Errorf("attempt %d carried %d bytes, want 10; a streamed body would be "+
				"drained by the first attempt and the retry would upload nothing", i+1, n)
		}
	}
}
