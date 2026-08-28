package hardiff

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
)

// Options controls what [Diff] compares and how much of a value it prints.
// The zero value is a sensible default.
type Options struct {
	// IgnoreHeaders names headers to leave out, case-insensitively. Nothing is
	// ignored by default.
	//
	// The temptation is to ship a list of headers that "always differ", and it
	// is worth resisting. Cookies, tokens and user agents differ between two
	// captures precisely when the difference is the answer: one capture being
	// signed in and the other not is exactly the sort of thing people spend a
	// day failing to notice. Hiding it by default would be the tool making that
	// mistake on the reader's behalf.
	IgnoreHeaders []string

	// ResponseHeaders compares the response headers too. Off by default,
	// because they carry a date, a request id and a cache state on almost every
	// response, and a diff of two captures is usually a question about what was
	// sent.
	ResponseHeaders bool

	// ResponseBodies compares response bodies. Off by default: they are large,
	// often absent from an export, and rarely the question.
	ResponseBodies bool

	// Redact reports which fields differ without printing either value, so the
	// result can be pasted somewhere. Captures are full of credentials and a
	// diff of two of them is mostly credentials.
	Redact bool

	// MaxValue truncates a printed value. 0 means 120 characters. Bodies and
	// tokens run to kilobytes and a diff nobody can read is not an answer.
	MaxValue int
}

func (o Options) maxValue() int {
	if o.MaxValue <= 0 {
		return 120
	}
	return o.MaxValue
}

func (o Options) ignored(name string) bool {
	for _, h := range o.IgnoreHeaders {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// Section says which part of a request or response a difference is in.
type Section string

const (
	SectionQuery     Section = "query"
	SectionHeader    Section = "header"
	SectionBody      Section = "body"
	SectionStatus    Section = "status"
	SectionRespHead  Section = "response header"
	SectionRespBody  Section = "response body"
	SectionHTTPVer   Section = "http version"
	SectionMimeType  Section = "content type"
	SectionBodyBytes Section = "body size"
)

// Field is one difference between two matched requests.
//
// InA and InB record presence, which is not the same question as the value.
// A header sent with an empty value and a header not sent at all are different
// requests, and telling them apart is most of what this is for.
type Field struct {
	Section Section
	Name    string
	A       string
	B       string
	InA     bool
	InB     bool
}

// Changed is a request present in both captures, with the fields that differ.
type Changed struct {
	Pair
	Fields []Field
}

// Result is what [Diff] found.
type Result struct {
	OnlyInA []Pair
	OnlyInB []Pair
	Changed []Changed

	// Same counts the matched requests with no differences, so a run that finds
	// nothing can say how much it looked at. "No differences" and "nothing was
	// compared" print the same way otherwise, and they are opposite answers.
	Same int
}

// Empty reports whether the two captures agree on everything compared.
func (r Result) Empty() bool {
	return len(r.OnlyInA) == 0 && len(r.OnlyInB) == 0 && len(r.Changed) == 0
}

// Diff compares two captures.
func Diff(a, b *HAR, opts Options) Result {
	var res Result
	for _, p := range Match(a, b) {
		switch {
		case p.A == nil:
			res.OnlyInB = append(res.OnlyInB, p)
		case p.B == nil:
			res.OnlyInA = append(res.OnlyInA, p)
		default:
			if fields := compare(*p.A, *p.B, opts); len(fields) > 0 {
				res.Changed = append(res.Changed, Changed{Pair: p, Fields: fields})
			} else {
				res.Same++
			}
		}
	}
	return res
}

func compare(a, b Entry, opts Options) []Field {
	var out []Field

	out = append(out, diffPairs(SectionQuery, queryOf(a.Request), queryOf(b.Request), opts)...)
	out = append(out, diffPairs(SectionHeader, named(a.Request.Headers), named(b.Request.Headers), opts)...)
	out = append(out, diffBody(SectionBody, a.Request.PostData, b.Request.PostData, opts)...)

	if a.Response.Status != b.Response.Status {
		out = append(out, Field{
			Section: SectionStatus,
			A:       strconv.Itoa(a.Response.Status),
			B:       strconv.Itoa(b.Response.Status),
			InA:     true,
			InB:     true,
		})
	}
	if opts.ResponseHeaders {
		out = append(out, diffPairs(SectionRespHead, named(a.Response.Headers), named(b.Response.Headers), opts)...)
	}
	if opts.ResponseBodies {
		out = append(out, diffBody(SectionRespBody, contentAsPost(a.Response.Content), contentAsPost(b.Response.Content), opts)...)
	}
	return out
}

// queryOf reads the query out of the URL rather than trusting queryString.
//
// The field is optional in the format and exporters disagree about filling it
// in, so a capture with a query in the URL and nothing in queryString is
// ordinary. Reading the URL means the two captures are read the same way even
// when they came out of different tools.
func queryOf(r Request) map[string][]string {
	u, err := neturl.Parse(r.URL)
	if err == nil && u.RawQuery != "" {
		return u.Query()
	}
	out := map[string][]string{}
	for _, nv := range r.QueryString {
		out[nv.Name] = append(out[nv.Name], nv.Value)
	}
	return out
}

func named(list []NameValue) map[string][]string {
	out := map[string][]string{}
	for _, nv := range list {
		out[strings.ToLower(nv.Name)] = append(out[strings.ToLower(nv.Name)], nv.Value)
	}
	return out
}

// diffPairs compares two name to values maps. A name repeated on both sides is
// compared as a joined list rather than pairwise, since order between repeats
// of one header is not something a capture reliably preserves.
func diffPairs(section Section, a, b map[string][]string, opts Options) []Field {
	var out []Field
	for _, name := range unionKeys(a, b) {
		if section != SectionQuery && opts.ignored(name) {
			continue
		}
		av, inA := a[name]
		bv, inB := b[name]
		as, bs := strings.Join(av, ", "), strings.Join(bv, ", ")
		if inA && inB && as == bs {
			continue
		}
		out = append(out, Field{
			Section: section,
			Name:    name,
			A:       clip(as, opts),
			B:       clip(bs, opts),
			InA:     inA,
			InB:     inB,
		})
	}
	return out
}

func diffBody(section Section, a, b *PostData, opts Options) []Field {
	at, bt := bodyText(a), bodyText(b)
	if at == bt {
		return nil
	}

	// Both JSON, so report the fields rather than the blob. This is the case
	// that matters: two captures of one endpoint usually differ in a handful of
	// keys inside a body of several kilobytes, and a whole-body comparison
	// answers "they differ" to a question nobody asked.
	af, aok := flattenJSON(at)
	bf, bok := flattenJSON(bt)
	if aok && bok {
		return diffPairs(section, af, bf, opts)
	}

	return []Field{{
		Section: section,
		Name:    describeBody(a, b),
		A:       clip(at, opts),
		B:       clip(bt, opts),
		InA:     at != "",
		InB:     bt != "",
	}}
}

func describeBody(a, b *PostData) string {
	am, bm := mimeOf(a), mimeOf(b)
	if am != "" && am == bm {
		return am
	}
	if am == "" || bm == "" {
		return "body"
	}
	return am + " / " + bm
}

func mimeOf(p *PostData) string {
	if p == nil {
		return ""
	}
	// A charset makes two identical types look different, and nobody diffing
	// captures cares which one the exporter wrote down.
	return strings.TrimSpace(strings.Split(p.MimeType, ";")[0])
}

func bodyText(p *PostData) string {
	if p == nil {
		return ""
	}
	if p.Text != "" {
		return p.Text
	}
	if len(p.Params) > 0 {
		parts := make([]string, 0, len(p.Params))
		for _, nv := range p.Params {
			parts = append(parts, nv.Name+"="+nv.Value)
		}
		sort.Strings(parts)
		return strings.Join(parts, "&")
	}
	return ""
}

func contentAsPost(c *Content) *PostData {
	if c == nil {
		return nil
	}
	// Base64 is left alone rather than decoded. It is usually an image or a
	// compressed blob, and a byte-level diff of one is noise.
	return &PostData{MimeType: c.MimeType, Text: c.Text}
}

// flattenJSON turns a JSON document into path to value, so a difference can be
// named as tickets.0.secname rather than as two kilobytes of text.
func flattenJSON(s string) (map[string][]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	out := map[string][]string{}
	walkJSON("", v, out)
	return out, true
}

func walkJSON(path string, v any, out map[string][]string) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			out[join(path, "")] = []string{"{}"}
			return
		}
		for _, k := range sortedKeys(t) {
			walkJSON(join(path, k), t[k], out)
		}
	case []any:
		if len(t) == 0 {
			out[join(path, "")] = []string{"[]"}
			return
		}
		for i, e := range t {
			walkJSON(join(path, strconv.Itoa(i)), e, out)
		}
	default:
		out[join(path, "")] = []string{scalar(t)}
	}
}

func join(path, key string) string {
	switch {
	case path == "" && key == "":
		return "(root)"
	case path == "":
		return key
	case key == "":
		return path
	}
	return path + "." + key
}

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// JSON has one number type, so an integer arrives as a float and would
		// print as 4.5e+03 without this. A capture is full of ids and amounts
		// and none of them should render in scientific notation.
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionKeys(a, b map[string][]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = true
		out = append(out, k)
	}
	for k := range b {
		if !seen[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func clip(s string, opts Options) string {
	if opts.Redact {
		if s == "" {
			return ""
		}
		return fmt.Sprintf("(%d bytes, redacted)", len(s))
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", " ")
	if max := opts.maxValue(); len(s) > max {
		return s[:max] + fmt.Sprintf("... (%d bytes)", len(s))
	}
	return s
}
