// Command hardiff compares two HTTP Archive captures and reports what changed.
//
//	hardiff before.har after.har
//	hardiff -redact before.har after.har
//	hardiff -host checkout.example.com a.har b.har
//	hardiff -ignore cookie -ignore user-agent a.har b.har
//	hardiff -json a.har b.har | jq '.changed[].fields[] | select(.section == "body")'
//
// Exits 0 when the captures agree, 1 when they differ, and 2 on an error, the
// same way diff(1) does.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mootjelh/hardiff"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var ignore stringList
	flag.Var(&ignore, "ignore", "leave out this `header`, case-insensitively. Repeatable")
	var hosts stringList
	flag.Var(&hosts, "host", "compare only requests to this `host`, parent domains included. Repeatable")
	respHeaders := flag.Bool("response-headers", false, "compare response headers too")
	respBodies := flag.Bool("response-bodies", false, "compare response bodies too")
	redact := flag.Bool("redact", false, "say which fields differ without printing either value")
	maxValue := flag.Int("max-value", 120, "truncate a printed value to this many `bytes`")
	asJSON := flag.Bool("json", false, "emit JSON instead of the text report")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: hardiff [flags] before.har after.har

Compares two HTTP Archive captures: which requests one made and the other did
not, and for the requests both made, which headers, query parameters and body
fields differ.

Requests are matched on method and URL path, with the query string compared as
a field rather than used to match. Otherwise a cache buster or a request id in
the query makes every request look like one removed and one added.

A real capture is mostly other people's traffic: payment iframes, consent
banners and analytics differ on every session id they carry and are never the
question. Use -host to narrow to the site you are actually looking at.

Nothing is ignored by default, including cookies and tokens. Those differ
exactly when the difference is the answer. Use -ignore to drop what you have
already accounted for, and -redact when the output is going somewhere else.

Exits 0 when the captures agree, 1 when they differ, and 2 on an error.

flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}

	pathA, pathB := flag.Arg(0), flag.Arg(1)
	a, err := hardiff.Load(pathA)
	if err != nil {
		fail(err)
	}
	b, err := hardiff.Load(pathB)
	if err != nil {
		fail(err)
	}

	res := hardiff.Diff(a, b, hardiff.Options{
		IgnoreHeaders:   ignore,
		Hosts:           hosts,
		ResponseHeaders: *respHeaders,
		ResponseBodies:  *respBodies,
		Redact:          *redact,
		MaxValue:        *maxValue,
	})

	if *asJSON {
		emitJSON(res, pathA, pathB)
	} else {
		fmt.Print(res.Render(filepath.Base(pathA), filepath.Base(pathB)))
	}

	if !res.Empty() {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hardiff:", err)
	os.Exit(2)
}

// --- JSON --------------------------------------------------------------------

type doc struct {
	A       string    `json:"a"`
	B       string    `json:"b"`
	Matched int       `json:"matched"`
	Same    int       `json:"same"`
	OnlyInA []request `json:"only_in_a"`
	OnlyInB []request `json:"only_in_b"`
	Changed []changed `json:"changed"`
}

type request struct {
	Key    string `json:"key"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Index  int    `json:"index"`
}

type changed struct {
	Key    string  `json:"key"`
	URL    string  `json:"url"`
	AIndex int     `json:"a_index"`
	BIndex int     `json:"b_index"`
	Fields []field `json:"fields"`
}

type field struct {
	Section string `json:"section"`
	Name    string `json:"name"`

	// Pointers so that absent and empty stay distinguishable in JSON. A field
	// not sent and a field sent empty are different requests, and null against
	// "" is the only way to say which this was.
	A *string `json:"a"`
	B *string `json:"b"`
}

func emitJSON(res hardiff.Result, pathA, pathB string) {
	d := doc{
		A:       pathA,
		B:       pathB,
		Matched: res.Same + len(res.Changed),
		Same:    res.Same,
		OnlyInA: []request{},
		OnlyInB: []request{},
		Changed: []changed{},
	}
	for _, p := range res.OnlyInA {
		d.OnlyInA = append(d.OnlyInA, request{Key: p.Key, Method: p.A.Request.Method, URL: p.A.Request.URL, Index: p.AIndex})
	}
	for _, p := range res.OnlyInB {
		d.OnlyInB = append(d.OnlyInB, request{Key: p.Key, Method: p.B.Request.Method, URL: p.B.Request.URL, Index: p.BIndex})
	}
	for _, c := range res.Changed {
		out := changed{Key: c.Key, URL: c.A.Request.URL, AIndex: c.AIndex, BIndex: c.BIndex, Fields: []field{}}
		for _, f := range c.Fields {
			out.Fields = append(out.Fields, field{
				Section: string(f.Section),
				Name:    f.Name,
				A:       present(f.A, f.InA),
				B:       present(f.B, f.InB),
			})
		}
		d.Changed = append(d.Changed, out)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		fail(err)
	}
}

func present(v string, ok bool) *string {
	if !ok {
		return nil
	}
	return &v
}
