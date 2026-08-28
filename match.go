package hardiff

import (
	neturl "net/url"
	"strings"
)

// Pair is one request found in both captures, or in only one of them.
// A nil A means it appears only in B, and the other way round.
type Pair struct {
	Key string
	A   *Entry
	B   *Entry

	// AIndex and BIndex are positions in the original entry lists, so a
	// finding can be pointed back at a line in the capture. They are -1 on the
	// side where the request is absent.
	AIndex int
	BIndex int
}

// Key identifies a request across two captures: the method, and the URL with
// its query string removed.
//
// Dropping the query is the whole reason this works. Matching on the full URL
// looks obviously right and is what makes a naive diff useless: a cache buster,
// a request id or a session token in the query means the same request appears
// under two different keys, so every one of them is reported as a request
// removed and an unrelated one added, and the actual difference is buried. The
// query is compared as a field instead, which is where a change in it belongs.
//
// A URL that will not parse is used as it is. It is somebody's capture, not
// necessarily somebody's valid URL, and refusing to match is worse than
// matching on a slightly odd key.
func Key(method, url string) string {
	u, err := neturl.Parse(url)
	if err != nil {
		return strings.ToUpper(method) + " " + url
	}
	u.RawQuery = ""
	u.Fragment = ""
	return strings.ToUpper(method) + " " + u.String()
}

// Match lines up the entries of two captures.
//
// Requests sharing a key are paired in the order they were recorded, first with
// first. That is a guess, and it is the right one: a page that calls the same
// endpoint several times does so in a stable order, and the alternative, some
// similarity score over bodies, would pair requests differently depending on
// their contents and make the output move around for reasons the reader cannot
// see.
//
// The result is ordered by where the request appears in A, and requests only in
// B come last, so reading it top to bottom follows the first capture.
func Match(a, b *HAR) []Pair {
	byKeyB := map[string][]int{}
	for i := range b.Log.Entries {
		e := &b.Log.Entries[i]
		k := Key(e.Request.Method, e.Request.URL)
		byKeyB[k] = append(byKeyB[k], i)
	}

	usedB := make([]bool, len(b.Log.Entries))
	var out []Pair

	for i := range a.Log.Entries {
		e := &a.Log.Entries[i]
		k := Key(e.Request.Method, e.Request.URL)

		if idxs := byKeyB[k]; len(idxs) > 0 {
			j := idxs[0]
			byKeyB[k] = idxs[1:]
			usedB[j] = true
			out = append(out, Pair{Key: k, A: e, B: &b.Log.Entries[j], AIndex: i, BIndex: j})
			continue
		}
		out = append(out, Pair{Key: k, A: e, AIndex: i, BIndex: -1})
	}

	for j := range b.Log.Entries {
		if usedB[j] {
			continue
		}
		e := &b.Log.Entries[j]
		out = append(out, Pair{
			Key:    Key(e.Request.Method, e.Request.URL),
			B:      e,
			AIndex: -1,
			BIndex: j,
		})
	}
	return out
}
