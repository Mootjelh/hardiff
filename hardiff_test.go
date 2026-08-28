package hardiff

import (
	"strings"
	"testing"
)

// Fixtures are built in code rather than checked in as .har files. A capture is
// somebody's session: it carries their cookies, their tokens and often their
// name and address, and none of that belongs in a public repository.

func nv(pairs ...string) []NameValue {
	out := make([]NameValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, NameValue{Name: pairs[i], Value: pairs[i+1]})
	}
	return out
}

type opt func(*Entry)

func headers(pairs ...string) opt {
	return func(e *Entry) { e.Request.Headers = nv(pairs...) }
}

func body(mime, text string) opt {
	return func(e *Entry) { e.Request.PostData = &PostData{MimeType: mime, Text: text} }
}

func status(code int) opt {
	return func(e *Entry) { e.Response.Status = code }
}

func respHeaders(pairs ...string) opt {
	return func(e *Entry) { e.Response.Headers = nv(pairs...) }
}

func respBody(mime, text string) opt {
	return func(e *Entry) { e.Response.Content = &Content{MimeType: mime, Text: text} }
}

func entry(method, url string, opts ...opt) Entry {
	e := Entry{Request: Request{Method: method, URL: url}, Response: Response{Status: 200}}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func har(entries ...Entry) *HAR {
	h := &HAR{}
	h.Log.Version = "1.2"
	h.Log.Entries = entries
	return h
}

// fieldsFor pulls the differences for one request out of a result, so a test
// can say what it means rather than index into a slice.
func fieldsFor(t *testing.T, r Result, key string) []Field {
	t.Helper()
	for _, c := range r.Changed {
		if c.Key == key {
			return c.Fields
		}
	}
	t.Fatalf("no changed request with key %q, got %v", key, changedKeys(r))
	return nil
}

func changedKeys(r Result) []string {
	out := make([]string, 0, len(r.Changed))
	for _, c := range r.Changed {
		out = append(out, c.Key)
	}
	return out
}

func find(fields []Field, section Section, name string) (Field, bool) {
	for _, f := range fields {
		if f.Section == section && f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// --- parsing -----------------------------------------------------------------

func TestParse(t *testing.T) {
	h, err := Parse(strings.NewReader(`{"log":{"version":"1.2","entries":[
		{"request":{"method":"get","url":"https://x.test/a"},"response":{"status":200}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(h.Log.Entries); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
	if got := h.Log.Entries[0].Request.URL; got != "https://x.test/a" {
		t.Errorf("url = %q", got)
	}
}

// A JSON file that is not a capture has to be named as such. Returning an
// archive with no entries makes it indistinguishable from a capture that
// recorded nothing, and those two want opposite reactions from the reader.
func TestParseRejectsJSONThatIsNotAHAR(t *testing.T) {
	_, err := Parse(strings.NewReader(`{"hello":1}`))
	if err == nil {
		t.Fatal("a JSON document with no log.entries was accepted")
	}
	if !strings.Contains(err.Error(), "not a HAR") {
		t.Errorf("err = %v, want it to say this is not a HAR", err)
	}
}

func TestParseRejectsBrokenJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader(`{`)); err == nil {
		t.Fatal("broken JSON was accepted")
	}
}

// An empty entries list is a real capture that recorded nothing, so it parses.
func TestParseAcceptsACaptureWithNoEntries(t *testing.T) {
	if _, err := Parse(strings.NewReader(`{"log":{"version":"1.2","entries":[]}}`)); err != nil {
		t.Errorf("an empty capture was rejected: %v", err)
	}
}

func TestHeaderLookup(t *testing.T) {
	r := Request{Headers: nv("Content-Type", "application/json", "X-Empty", "")}

	if v, ok := r.Header("content-type"); !ok || v != "application/json" {
		t.Errorf("Header(content-type) = %q, %v", v, ok)
	}
	// Sent empty and not sent at all are different requests.
	if v, ok := r.Header("x-empty"); !ok || v != "" {
		t.Errorf("Header(x-empty) = %q, %v, want an empty value that is present", v, ok)
	}
	if _, ok := r.Header("x-absent"); ok {
		t.Error("Header(x-absent) reported present")
	}
}

// --- matching ----------------------------------------------------------------

// The core of the tool. Matching on the full URL is the obvious thing and it is
// what makes a naive diff useless: a cache buster turns one request into one
// removed and one added, and the real difference is buried in the noise.
func TestKeyIgnoresTheQuery(t *testing.T) {
	a := Key("GET", "https://x.test/api/rules?eventId=abc&_=111")
	b := Key("GET", "https://x.test/api/rules?eventId=abc&_=999")
	if a != b {
		t.Errorf("two requests to one endpoint keyed differently:\n %q\n %q", a, b)
	}
	if got := Key("get", "https://x.test/api/rules"); got != a {
		t.Errorf("method case changed the key: %q vs %q", got, a)
	}
	if Key("POST", "https://x.test/api/rules") == a {
		t.Error("method is not part of the key")
	}
	if Key("GET", "https://x.test/other") == a {
		t.Error("path is not part of the key")
	}
}

func TestKeyOnAURLThatWillNotParse(t *testing.T) {
	// Somebody's capture, not necessarily somebody's valid URL. Refusing to
	// match is worse than matching on an odd key.
	got := Key("GET", "://nonsense")
	if got == "" {
		t.Error("an unparseable URL produced an empty key")
	}
}

func TestMatchPairsAndLeftovers(t *testing.T) {
	a := har(
		entry("GET", "https://x.test/a"),
		entry("GET", "https://x.test/only-a"),
	)
	b := har(
		entry("GET", "https://x.test/only-b"),
		entry("GET", "https://x.test/a"),
	)

	pairs := Match(a, b)
	if len(pairs) != 3 {
		t.Fatalf("got %d pairs, want 3", len(pairs))
	}
	// Ordered by where the request appears in A, with B-only last, so reading
	// top to bottom follows the first capture.
	if pairs[0].A == nil || pairs[0].B == nil {
		t.Errorf("pair 0 = %+v, want the matched one first", pairs[0])
	}
	if pairs[1].B != nil || !strings.Contains(pairs[1].Key, "only-a") {
		t.Errorf("pair 1 = %s, want only-a with no B side", pairs[1].Key)
	}
	if pairs[2].A != nil || !strings.Contains(pairs[2].Key, "only-b") {
		t.Errorf("pair 2 = %s, want only-b with no A side", pairs[2].Key)
	}
}

// A page that calls one endpoint several times is ordinary, and the repeats
// have to line up first with first rather than all collapsing onto one.
func TestMatchPairsRepeatsInOrder(t *testing.T) {
	a := har(
		entry("POST", "https://x.test/gql", body("application/json", `{"op":"one"}`)),
		entry("POST", "https://x.test/gql", body("application/json", `{"op":"two"}`)),
	)
	b := har(
		entry("POST", "https://x.test/gql", body("application/json", `{"op":"one"}`)),
		entry("POST", "https://x.test/gql", body("application/json", `{"op":"CHANGED"}`)),
	)

	res := Diff(a, b, Options{})
	if len(res.Changed) != 1 || res.Same != 1 {
		t.Fatalf("changed=%d same=%d, want 1 and 1: the repeats should pair up in order",
			len(res.Changed), res.Same)
	}
	if f, ok := find(res.Changed[0].Fields, SectionBody, "op"); !ok || f.A != `"two"` || f.B != `"CHANGED"` {
		t.Errorf("field = %+v, want the second call compared against the second", f)
	}
}

func TestMatchExtraRepeatsBecomeOnlyIn(t *testing.T) {
	a := har(
		entry("POST", "https://x.test/gql"),
		entry("POST", "https://x.test/gql"),
		entry("POST", "https://x.test/gql"),
	)
	b := har(entry("POST", "https://x.test/gql"))

	res := Diff(a, b, Options{})
	if len(res.OnlyInA) != 2 {
		t.Errorf("only in A = %d, want 2: three calls against one leaves two unmatched", len(res.OnlyInA))
	}
	if res.Same != 1 {
		t.Errorf("same = %d, want 1", res.Same)
	}
}

// --- differences -------------------------------------------------------------

func TestDiffQueryIsComparedRatherThanMatchedOn(t *testing.T) {
	a := har(entry("GET", "https://x.test/r?eventId=abc&_=111"))
	b := har(entry("GET", "https://x.test/r?eventId=abc&_=999"))

	res := Diff(a, b, Options{})
	if len(res.OnlyInA) != 0 || len(res.OnlyInB) != 0 {
		t.Fatalf("a cache buster split one request into two: onlyA=%d onlyB=%d",
			len(res.OnlyInA), len(res.OnlyInB))
	}
	fields := fieldsFor(t, res, Key("GET", "https://x.test/r"))
	if len(fields) != 1 {
		t.Fatalf("fields = %+v, want only the query parameter that moved", fields)
	}
	f := fields[0]
	if f.Section != SectionQuery || f.Name != "_" || f.A != "111" || f.B != "999" {
		t.Errorf("field = %+v", f)
	}
}

func TestDiffHeaders(t *testing.T) {
	a := har(entry("GET", "https://x.test/a", headers("X-Cmd", "reserve", "Accept", "*/*", "X-Empty", "")))
	b := har(entry("GET", "https://x.test/a", headers("accept", "*/*", "X-New", "1")))

	fields := fieldsFor(t, Diff(a, b, Options{}), Key("GET", "https://x.test/a"))

	cmd, ok := find(fields, SectionHeader, "x-cmd")
	if !ok || !cmd.InA || cmd.InB {
		t.Errorf("x-cmd = %+v, want present in A and absent in B", cmd)
	}
	if _, ok := find(fields, SectionHeader, "accept"); ok {
		t.Error("accept is the same on both sides and should not be reported, case notwithstanding")
	}
	// Sent empty on one side and not sent on the other is a difference, and the
	// point of tracking presence separately from value.
	empty, ok := find(fields, SectionHeader, "x-empty")
	if !ok || !empty.InA || empty.InB || empty.A != "" {
		t.Errorf("x-empty = %+v, want an empty value present in A only", empty)
	}
	if n, ok := find(fields, SectionHeader, "x-new"); !ok || n.InA || !n.InB {
		t.Errorf("x-new = %+v, want absent in A and present in B", n)
	}
}

func TestDiffJSONBodyReportsFieldsNotTheBlob(t *testing.T) {
	a := har(entry("POST", "https://x.test/gql", body("application/json",
		`{"in":{"tickets":[{"section":"F","secname":["P27"],"quantity":2}]}}`)))
	b := har(entry("POST", "https://x.test/gql", body("application/json",
		`{"in":{"tickets":[{"section":"F","startSeat":"8","quantity":2}]}}`)))

	fields := fieldsFor(t, Diff(a, b, Options{}), Key("POST", "https://x.test/gql"))

	if _, ok := find(fields, SectionBody, "in.tickets.0.section"); ok {
		t.Error("an unchanged body field was reported")
	}
	if f, ok := find(fields, SectionBody, "in.tickets.0.secname.0"); !ok || !f.InA || f.InB {
		t.Errorf("secname = %+v, want present in A only", f)
	}
	if f, ok := find(fields, SectionBody, "in.tickets.0.startSeat"); !ok || f.InA || !f.InB {
		t.Errorf("startSeat = %+v, want present in B only", f)
	}
}

// JSON has one number type, so an id arrives as a float and prints in
// scientific notation unless it is formatted. A capture is mostly ids.
//
// The ids here are nine digits on purpose. Go's default float formatting only
// switches to an exponent above a certain magnitude, so a small number like an
// amount in cents renders the same either way and would make this test pass
// against the bug it is here to catch.
func TestDiffJSONNumbersAreNotScientific(t *testing.T) {
	a := har(entry("POST", "https://x.test/n", body("application/json",
		`{"systemEventId":1963224601,"amount":10680}`)))
	b := har(entry("POST", "https://x.test/n", body("application/json",
		`{"systemEventId":385233540,"amount":11550}`)))

	fields := fieldsFor(t, Diff(a, b, Options{}), Key("POST", "https://x.test/n"))

	id, ok := find(fields, SectionBody, "systemEventId")
	if !ok || id.A != "1963224601" || id.B != "385233540" {
		t.Errorf("systemEventId = %+v, want the digits rather than an exponent", id)
	}
	amount, ok := find(fields, SectionBody, "amount")
	if !ok || amount.A != "10680" || amount.B != "11550" {
		t.Errorf("amount = %+v, want plain integers", amount)
	}
}

func TestDiffNonJSONBody(t *testing.T) {
	a := har(entry("POST", "https://x.test/f", body("text/plain", "hello")))
	b := har(entry("POST", "https://x.test/f", body("text/plain", "goodbye")))

	fields := fieldsFor(t, Diff(a, b, Options{}), Key("POST", "https://x.test/f"))
	if len(fields) != 1 || fields[0].Section != SectionBody {
		t.Fatalf("fields = %+v, want one body difference", fields)
	}
	if fields[0].A != "hello" || fields[0].B != "goodbye" {
		t.Errorf("body = %+v", fields[0])
	}
}

func TestDiffStatus(t *testing.T) {
	a := har(entry("GET", "https://x.test/s", status(200)))
	b := har(entry("GET", "https://x.test/s", status(403)))

	fields := fieldsFor(t, Diff(a, b, Options{}), Key("GET", "https://x.test/s"))
	f, ok := find(fields, SectionStatus, "")
	if !ok || f.A != "200" || f.B != "403" {
		t.Errorf("status = %+v", f)
	}
}

func TestIgnoreHeaders(t *testing.T) {
	a := har(entry("GET", "https://x.test/i", headers("Cookie", "sid=1", "X-Real", "a")))
	b := har(entry("GET", "https://x.test/i", headers("Cookie", "sid=2", "X-Real", "b")))

	// The control first. Without the option both differences are reported, so
	// a passing test below means the option worked rather than that the
	// difference was never there.
	if fields := fieldsFor(t, Diff(a, b, Options{}), Key("GET", "https://x.test/i")); len(fields) != 2 {
		t.Fatalf("without -ignore, fields = %+v, want both", fields)
	}

	fields := fieldsFor(t, Diff(a, b, Options{IgnoreHeaders: []string{"COOKIE"}}), Key("GET", "https://x.test/i"))
	if len(fields) != 1 || fields[0].Name != "x-real" {
		t.Errorf("fields = %+v, want cookie dropped case-insensitively and x-real kept", fields)
	}
}

func TestResponseHeadersAreOffByDefault(t *testing.T) {
	a := har(entry("GET", "https://x.test/rh", respHeaders("Date", "Mon")))
	b := har(entry("GET", "https://x.test/rh", respHeaders("Date", "Tue")))

	if res := Diff(a, b, Options{}); !res.Empty() {
		t.Errorf("response headers were compared by default: %+v", res.Changed)
	}
	res := Diff(a, b, Options{ResponseHeaders: true})
	if len(res.Changed) != 1 {
		t.Fatalf("with ResponseHeaders the difference was not found: %+v", res)
	}
	if f, ok := find(res.Changed[0].Fields, SectionRespHead, "date"); !ok || f.A != "Mon" {
		t.Errorf("field = %+v", f)
	}
}

func TestResponseBodiesAreOffByDefault(t *testing.T) {
	a := har(entry("GET", "https://x.test/rb", respBody("application/json", `{"a":1}`)))
	b := har(entry("GET", "https://x.test/rb", respBody("application/json", `{"a":2}`)))

	if res := Diff(a, b, Options{}); !res.Empty() {
		t.Errorf("response bodies were compared by default: %+v", res.Changed)
	}
	res := Diff(a, b, Options{ResponseBodies: true})
	if len(res.Changed) != 1 {
		t.Fatalf("with ResponseBodies the difference was not found: %+v", res)
	}
	if f, ok := find(res.Changed[0].Fields, SectionRespBody, "a"); !ok || f.A != "1" || f.B != "2" {
		t.Errorf("field = %+v", f)
	}
}

// Redacting has to keep the finding and drop the value, or it is useless for
// the thing it is for: pasting a diff of two captures somewhere.
func TestRedactKeepsTheFindingAndDropsTheValue(t *testing.T) {
	secret := "eyJhbGciOiJSUzI1NiJ9.aaaaaaaa"
	a := har(entry("GET", "https://x.test/r", headers("Authorization", "Bearer "+secret)))
	b := har(entry("GET", "https://x.test/r", headers("Authorization", "Bearer other")))

	res := Diff(a, b, Options{Redact: true})
	fields := fieldsFor(t, res, Key("GET", "https://x.test/r"))
	f, ok := find(fields, SectionHeader, "authorization")
	if !ok {
		t.Fatal("redacting lost the finding")
	}
	if strings.Contains(f.A, secret) || strings.Contains(f.B, "other") {
		t.Errorf("redacting printed the value anyway: %+v", f)
	}
	if !strings.Contains(f.A, "redacted") {
		t.Errorf("A = %q, want it to say the value was withheld", f.A)
	}
}

func TestMaxValueTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	a := har(entry("GET", "https://x.test/t", headers("X-Long", long)))
	b := har(entry("GET", "https://x.test/t", headers("X-Long", "short")))

	fields := fieldsFor(t, Diff(a, b, Options{MaxValue: 20}), Key("GET", "https://x.test/t"))
	f, _ := find(fields, SectionHeader, "x-long")
	if len(f.A) > 60 {
		t.Errorf("value was not truncated: %d bytes", len(f.A))
	}
	if !strings.Contains(f.A, "500 bytes") {
		t.Errorf("A = %q, want the real size named so the number is not lost", f.A)
	}
}

func TestSameCounts(t *testing.T) {
	a := har(
		entry("GET", "https://x.test/1"),
		entry("GET", "https://x.test/2", headers("X", "a")),
	)
	b := har(
		entry("GET", "https://x.test/1"),
		entry("GET", "https://x.test/2", headers("X", "b")),
	)

	res := Diff(a, b, Options{})
	if res.Same != 1 || len(res.Changed) != 1 {
		t.Errorf("same=%d changed=%d, want 1 and 1", res.Same, len(res.Changed))
	}
	if res.Empty() {
		t.Error("Empty() was true with a change present")
	}
}

// --- rendering ---------------------------------------------------------------

// Nothing found and nothing compared print the same way unless the count is
// there, and they are opposite answers: one says the captures agree, the other
// says they have no request in common.
func TestRenderSaysHowMuchItCompared(t *testing.T) {
	a := har(entry("GET", "https://x.test/1"))
	out := Diff(a, a, Options{}).Render("a.har", "b.har")

	if !strings.Contains(out, "no differences across 1 matched request") {
		t.Errorf("out = %q, want it to say how many requests were compared", out)
	}

	none := Diff(har(entry("GET", "https://x.test/1")), har(), Options{}).Render("a.har", "b.har")
	if strings.Contains(none, "no differences") {
		t.Errorf("out = %q, two captures with nothing in common are not agreement", none)
	}
}

func TestRenderSpellsOutAbsent(t *testing.T) {
	a := har(entry("GET", "https://x.test/1", headers("X-Cmd", "reserve")))
	b := har(entry("GET", "https://x.test/1"))

	out := Diff(a, b, Options{}).Render("a.har", "b.har")
	if !strings.Contains(out, "(absent)") {
		t.Errorf("out = %q, want an absent field to say so rather than print blank", out)
	}
}
