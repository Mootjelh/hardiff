// Package hardiff compares two HTTP Archive captures and reports what changed.
//
// A HAR is a recording of what a browser or a client actually sent, and the
// usual reason to hold two of them is a question of the form "this worked
// yesterday and does not today", or "the browser gets through and my client
// does not". Answering that means finding the same request in both captures and
// looking at the fields, which is tedious by hand and is what this does.
//
// The part that makes it work is the matching. Two captures of one flow never
// line up by position: requests arrive in a different order, cache busters and
// request ids differ, and one capture usually has entries the other does not.
// See [Match].
//
// Nothing here fetches anything or writes to a capture. It reads JSON and
// returns a description.
package hardiff

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// HAR is the subset of the HAR 1.2 format this package reads.
//
// Deliberately not the whole spec. Timings, cache entries, page groupings and
// the creator block say nothing about what was sent, and parsing fields nobody
// compares is a way to fail on a capture that would otherwise have worked.
// Unknown fields are ignored rather than rejected, because exporters disagree
// about the optional half of the format.
type HAR struct {
	Log struct {
		Version string  `json:"version"`
		Entries []Entry `json:"entries"`
	} `json:"log"`
}

// Entry is one request and its response.
type Entry struct {
	StartedDateTime string   `json:"startedDateTime"`
	Request         Request  `json:"request"`
	Response        Response `json:"response"`
}

// Request is what went out.
type Request struct {
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []NameValue `json:"headers"`
	QueryString []NameValue `json:"queryString"`
	Cookies     []NameValue `json:"cookies"`
	PostData    *PostData   `json:"postData"`
}

// Response is what came back.
type Response struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []NameValue `json:"headers"`
	Content     *Content    `json:"content"`
}

// PostData is a request body.
type PostData struct {
	MimeType string      `json:"mimeType"`
	Text     string      `json:"text"`
	Params   []NameValue `json:"params"`
}

// Content is a response body. Text may be base64 when Encoding says so, and may
// be absent entirely: plenty of exporters drop response bodies, and an absent
// body is not an empty one.
type Content struct {
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
	Encoding string `json:"encoding"`
}

// NameValue is a header, a query parameter or a cookie. All three are the same
// shape in a HAR.
type NameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Load reads a capture from disk.
func Load(path string) (*HAR, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return h, nil
}

// Parse reads a capture from a reader.
//
// A file that is JSON but is not a HAR is rejected by name rather than coming
// back as an archive with no entries. An empty result is the same shape as a
// capture that recorded nothing, and those two need opposite responses from
// whoever is reading the output.
func Parse(r io.Reader) (*HAR, error) {
	var h HAR
	if err := json.NewDecoder(r).Decode(&h); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if h.Log.Entries == nil {
		return nil, fmt.Errorf("no log.entries, so this is not a HAR capture")
	}
	return &h, nil
}

// Header returns the value of the named header, case-insensitively, and whether
// it was there at all.
//
// Absent and empty are different answers. A header sent with an empty value is
// a decision the client made; a missing one is a decision it did not.
func (r Request) Header(name string) (string, bool) { return lookup(r.Headers, name) }

// Header is [Request.Header] for a response.
func (r Response) Header(name string) (string, bool) { return lookup(r.Headers, name) }

func lookup(list []NameValue, name string) (string, bool) {
	for _, nv := range list {
		if strings.EqualFold(nv.Name, name) {
			return nv.Value, true
		}
	}
	return "", false
}
