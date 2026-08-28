package hardiff

import (
	"fmt"
	"strings"
)

// Render writes the result as text, with nameA and nameB used to label the two
// sides.
//
// Requests are grouped under the three questions someone actually has: what did
// the first capture do that the second did not, what did the second do that the
// first did not, and where they did the same thing, what was different about
// it.
func (r Result) Render(nameA, nameB string) string {
	var b strings.Builder

	if len(r.OnlyInA) > 0 {
		fmt.Fprintf(&b, "only in %s\n", nameA)
		for _, p := range r.OnlyInA {
			fmt.Fprintf(&b, "  %s\n", p.Key)
		}
		b.WriteString("\n")
	}
	if len(r.OnlyInB) > 0 {
		fmt.Fprintf(&b, "only in %s\n", nameB)
		for _, p := range r.OnlyInB {
			fmt.Fprintf(&b, "  %s\n", p.Key)
		}
		b.WriteString("\n")
	}

	if len(r.Changed) > 0 {
		b.WriteString("changed\n")
		for _, c := range r.Changed {
			fmt.Fprintf(&b, "  %s\n", c.Key)
			labelW, valueW := 0, 0
			for _, f := range c.Fields {
				if n := len(label(f)); n > labelW {
					labelW = n
				}
				if n := len(side(f.A, f.InA)); n > valueW {
					valueW = n
				}
			}
			// Capped, or one long token pushes every arrow off the right of the
			// terminal and the column stops being a column.
			if valueW > 48 {
				valueW = 48
			}
			for _, f := range c.Fields {
				fmt.Fprintf(&b, "    %-*s  %-*s  ->  %s\n",
					labelW, label(f), valueW, side(f.A, f.InA), side(f.B, f.InB))
			}
		}
		b.WriteString("\n")
	}

	if r.Empty() {
		// Saying how much was compared, because "no differences" and "the two
		// captures share no requests at all" are opposite answers and both
		// print as silence otherwise.
		fmt.Fprintf(&b, "no differences across %d matched request(s)\n", r.Same)
		return b.String()
	}

	fmt.Fprintf(&b, "%d changed, %d only in %s, %d only in %s, %d identical\n",
		len(r.Changed), len(r.OnlyInA), nameA, len(r.OnlyInB), nameB, r.Same)
	return b.String()
}

// label names the field. A section with nothing to qualify it, like a status
// code, prints as itself rather than as "status status".
func label(f Field) string {
	if f.Name == "" {
		return string(f.Section)
	}
	return string(f.Section) + " " + f.Name
}

// side renders one half of a difference. Absent is spelled out rather than left
// blank, since a field sent empty and a field not sent are different requests
// and a blank column cannot say which one this is.
func side(v string, present bool) string {
	if !present {
		return "(absent)"
	}
	if v == "" {
		return `""`
	}
	return v
}
