# hardiff

[![CI](https://github.com/Mootjelh/hardiff/actions/workflows/ci.yml/badge.svg)](https://github.com/Mootjelh/hardiff/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Mootjelh/hardiff.svg)](https://pkg.go.dev/github.com/Mootjelh/hardiff)

Compare two HTTP Archive captures and see what changed.

No dependencies.

## Why

You have two HARs of the same flow. It worked yesterday and not today, or the browser gets through and your client does not, or one capture is signed in and you cannot see where that shows up. The answer is in there, in a field, somewhere among a few hundred requests.

Doing it by hand means finding the same request in both files and reading the headers side by side. Doing it with `diff` does not work, because the two captures never line up: requests arrive in a different order, ids and timestamps differ on every line, and one capture always has entries the other does not.

```
$ hardiff before.har after.har
only in before.har
  GET https://example.test/only-in-a

only in after.har
  GET https://example.test/only-in-b

changed
  GET https://example.test/api/rules
    query _  111  ->  999
  POST https://example.test/graphql
    header x-cmd                           reserve   ->  (absent)
    body reserveInput.tickets.0.endSeat    (absent)  ->  "7"
    body reserveInput.tickets.0.priceZone  "27"      ->  "P5"
    body reserveInput.tickets.0.secname.0  "P27"     ->  (absent)
    body reserveInput.tickets.0.startSeat  (absent)  ->  "8"
    status                                 200       ->  403

2 changed, 1 only in before.har, 1 only in after.har, 0 identical
```

## Install

```bash
go install github.com/Mootjelh/hardiff/cmd/hardiff@latest
```

## The matching is the whole trick

Requests are matched on **method and URL path**, with the query string compared as a field rather than used for matching.

That one decision is what makes the output readable. Match on the full URL, which is the obvious thing to do, and a cache buster like `?_=1724876400` gives the same request two different keys. It then shows up as one request removed and one added, on every single call, and the difference you were looking for is buried under a hundred lines of noise. Above, `_=111` against `_=999` is reported as a query parameter that moved, which is what it is.

Several calls to the same endpoint are paired in the order they were recorded, first with first. A page that hits one endpoint repeatedly does so in a stable order, so this is right far more often than a similarity score over bodies, which would also make the output shuffle for reasons you cannot see.

## JSON bodies are compared as fields

Two captures of one endpoint usually differ in a handful of keys inside several kilobytes of JSON. Comparing the bodies as text answers "they differ", which you knew. So a body that parses as JSON on both sides is flattened and compared by path:

```
body reserveInput.tickets.0.secname.0  "P27"  ->  (absent)
```

Anything that is not JSON is compared as text.

## Absent is not empty

A header sent with an empty value and a header not sent at all are different requests, and a lot of the time that difference is the answer. So presence and value are tracked separately, and a missing field prints as `(absent)` rather than as a blank column. Under `-json` it is `null` against `""`.

## Nothing is ignored by default

The tempting thing is to ship a list of headers that "always differ", and it is worth resisting. Cookies, tokens and user agents differ between two captures exactly when the difference is the point. One capture being signed in and the other not is precisely the sort of thing people fail to notice for a day.

So the defaults show everything, and you drop what you have already accounted for:

```bash
hardiff -ignore cookie -ignore user-agent a.har b.har
```

Response headers and response bodies are off by default. Those carry a date, a request id and a cache state on almost every response, and the question is usually what was sent. `-response-headers` and `-response-bodies` turn them on.

## Sharing the result

A capture is full of credentials, and a diff of two captures is mostly credentials. `-redact` keeps the finding and drops the value, so the output can go in an issue:

```
header authorization  (36 bytes, redacted)  ->  (29 bytes, redacted)
```

## Scripting

```bash
hardiff -json a.har b.har | jq '.changed[].fields[] | select(.section == "body")'
```

Exits 0 when the captures agree, 1 when they differ, and 2 on an error, the same way `diff(1)` does.

## As a library

```go
a, err := hardiff.Load("before.har")
b, err := hardiff.Load("after.har")

res := hardiff.Diff(a, b, hardiff.Options{})
fmt.Print(res.Render("before.har", "after.har"))
```

`Match` is exported on its own, for pairing up two captures and then asking your own questions about the pairs.

## What it doesn't do

* No timing or waterfall comparison. Several tools already do that, and it is a different question from "what did I send differently".
* No HAR editing or replay. This reads and reports.
* No fuzzy matching of request bodies. Requests are matched by endpoint, not by content.

## A note on test fixtures

There are no `.har` files in this repository, and `.gitignore` refuses them. A capture is somebody's session: it carries their cookies, their tokens and often their name and address. The fixtures are built in code instead.

## License

MIT
