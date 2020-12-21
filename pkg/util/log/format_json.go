// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package log

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/util/jsonbytes"
	"github.com/cockroachdb/cockroach/pkg/util/log/logpb"
	"github.com/cockroachdb/redact"
)

type formatFluentJSONCompact struct{}

func (formatFluentJSONCompact) formatterName() string { return "json-fluent-compact" }

func (formatFluentJSONCompact) doc() string { return formatJSONDoc(true /* fluent */, tagCompact) }

func (f formatFluentJSONCompact) formatEntry(entry logEntry) *buffer {
	return formatJSON(entry, true /* fluent */, tagCompact)
}

type formatFluentJSONFull struct{}

func (formatFluentJSONFull) formatterName() string { return "json-fluent" }

func (f formatFluentJSONFull) formatEntry(entry logEntry) *buffer {
	return formatJSON(entry, true /* fluent */, tagVerbose)
}

func (formatFluentJSONFull) doc() string { return formatJSONDoc(true /* fluent */, tagVerbose) }

type formatJSONCompact struct{}

func (formatJSONCompact) formatterName() string { return "json-compact" }

func (f formatJSONCompact) formatEntry(entry logEntry) *buffer {
	return formatJSON(entry, false /* fluent */, tagCompact)
}

func (formatJSONCompact) doc() string { return formatJSONDoc(false /* fluent */, tagCompact) }

type formatJSONFull struct{}

func (formatJSONFull) formatterName() string { return "json" }

func (f formatJSONFull) formatEntry(entry logEntry) *buffer {
	return formatJSON(entry, false /* fluent */, tagVerbose)
}

func (formatJSONFull) doc() string { return formatJSONDoc(false /* fluent */, tagVerbose) }

func formatJSONDoc(forFluent bool, tags tagChoice) string {
	var buf strings.Builder
	buf.WriteString(`This format emits log entries as a JSON payload.

The JSON object is guaranteed to not contain unescaped newlines
or other special characters, and the entry as a whole is followed
by a newline character. This makes the format suitable for
processing over a stream unambiguously.

Each entry contains at least the following fields:

| Field | Description |
|-------|-------------|
`)
	if forFluent {
		buf.WriteString("| `tag` | A Fluent tag for the event, formed by the process name and the logging channel. |\n")
	}

	keys := make([]string, 0, len(jsonTags))
	for c := range jsonTags {
		keys = append(keys, string(c))
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := k[0]
		fmt.Fprintf(&buf, "| `%s` | %s |\n", jsonTags[c].tags[tags], jsonTags[c].description)
	}

	buf.WriteString(`

Additionally, the following fields are conditionally present:

| Field               | Description |
|---------------------|-------------|
| ` + "`tags`" + `    | The logging context tags for the entry, if there were context tags. |
| ` + "`message`" + ` | For unstructured events, the flat text payload. |
| ` + "`event`" + `   | The logging event, if structured (see below for details). |
| ` + "`stacks`" + `  | Goroutine stacks, for fatal events. |

When an entry is structured, the ` + "`event`" + ` field maps to a dictionary
whose structure is one of the documented structured events. See the reference
documentation for structured events for a list of possible payloads.

Then the entry is marked as "redactable", the ` + "`tags`, `message` and/or `event`" + ` payloads
contain delimiters (` + string(redact.StartMarker()) + `...` + string(redact.EndMarker()) + `) around
fields that are considered sensitive. These markers are automatically recognized
by ` + "`" + `debug zip` + "`" + ` and ` + "`" + `debug merge-logs` + "`" + ` when log redaction is requested.


`)

	return buf.String()
}

var jsonTags = map[byte]struct {
	tags        [2]string
	description string
}{
	'c': {[2]string{"c", "channel_numeric"},
		"The numeric identifier for the logging channel where the event was sent."},
	'C': {[2]string{"C", "channel"},
		"The name of the logging channel where the event was sent."},
	't': {[2]string{"t", "timestamp"},
		"The timestamp at which the event was emitted on the logging channel."},
	's': {[2]string{"s", "severity_numeric"},
		"The numeric value of the severity of the event."},
	'S': {[2]string{"sev", "severity"},
		"The severity of the event."},
	'g': {[2]string{"g", "goroutine"},
		"The identifier of the goroutine where the event was emitted."},
	'f': {[2]string{"f", "file"},
		"The name of the source file where the event was emitted."},
	'l': {[2]string{"l", "line"},
		"The line number where the event was emitted in the source."},
	'n': {[2]string{"n", "entry_counter"},
		"The entry number on this logging sink, relative to the last process restart."},
	'r': {[2]string{"r", "redactable"},
		"Whether the payload is redactable (see below for details)."},
}

type tagChoice int

const (
	tagCompact tagChoice = 0
	tagVerbose tagChoice = 1
)

var programEscaped = strings.ReplaceAll(program, ".", "_")

var channelNamesLowercase = func() map[Channel]string {
	lnames := make(map[Channel]string, len(logpb.Channel_name))
	for ch, s := range logpb.Channel_name {
		lnames[Channel(ch)] = strings.ToLower(s)
	}
	return lnames
}()

func formatJSON(entry logEntry, forFluent bool, tags tagChoice) *buffer {
	jtags := jsonTags
	buf := getBuffer()
	buf.WriteByte('{')
	if forFluent {
		// Tag: this is the main category for Fluentd events.
		buf.WriteString(`"tag":"`)
		// Note: fluent prefers if there is no period in the tag other
		// than the one splitting the application and category.
		buf.WriteString(programEscaped)
		buf.WriteByte('.')
		buf.WriteString(channelNamesLowercase[entry.ch])
		// Also include the channel number in numeric form to facilitate
		// automatic processing.
		buf.WriteString(`",`)
	}
	buf.WriteByte('"')
	buf.WriteString(jtags['c'].tags[tags])
	buf.WriteString(`":`)
	n := buf.someDigits(0, int(entry.ch))
	buf.Write(buf.tmp[:n])
	if tags != tagCompact {
		buf.WriteString(`,"`)
		buf.WriteString(jtags['C'].tags[tags])
		buf.WriteString(`":"`)
		escapeString(buf, entry.ch.String())
		buf.WriteByte('"')
	}
	// Timestamp.
	// Note: fluentd is particular about the time format; although this
	// looks like a float with a fractional number of seconds, fluentd
	// interprets the number after the period as a number of
	// nanoseconds. So for example "1.2" is interpreted as "2
	// nanoseconds after the second". So we really need to emit all 9
	// digits.
	// Also, we enclose the timestamp in double quotes because the
	// precision of the resulting number exceeds json's native float
	// precision. Fluentd doesn't care and still parses the value properly.
	buf.WriteString(`,"`)
	buf.WriteString(jtags['t'].tags[tags])
	buf.WriteString(`":"`)
	n = buf.someDigits(0, int(entry.ts/1000000000))
	buf.tmp[n] = '.'
	n++
	n += buf.nDigits(9, n, int(entry.ts%1000000000), '0')
	buf.Write(buf.tmp[:n])

	// Severity, both in numeric form (for ease of processing) and
	// string form (to facilitate human comprehension).
	buf.WriteString(`","`)
	buf.WriteString(jtags['s'].tags[tags])
	buf.WriteString(`":`)
	n = buf.someDigits(0, int(entry.sev))
	buf.Write(buf.tmp[:n])

	if tags == tagCompact {
		if entry.sev > 0 && int(entry.sev) <= len(severityChar) {
			buf.WriteString(`,"`)
			buf.WriteString(jtags['S'].tags[tags])
			buf.WriteString(`":"`)
			buf.WriteByte(severityChar[int(entry.sev)-1])
			buf.WriteByte('"')
		}
	} else {
		buf.WriteString(`,"`)
		buf.WriteString(jtags['S'].tags[tags])
		buf.WriteString(`":"`)
		escapeString(buf, entry.sev.String())
		buf.WriteByte('"')
	}

	// Goroutine number.
	buf.WriteString(`,"`)
	buf.WriteString(jtags['g'].tags[tags])
	buf.WriteString(`":`)
	n = buf.someDigits(0, int(entry.gid))
	buf.Write(buf.tmp[:n])

	// Source location.
	buf.WriteString(`,"`)
	buf.WriteString(jtags['f'].tags[tags])
	buf.WriteString(`":"`)
	escapeString(buf, entry.file)
	buf.WriteString(`","`)
	buf.WriteString(jtags['l'].tags[tags])
	buf.WriteString(`":`)
	n = buf.someDigits(0, entry.line)
	buf.Write(buf.tmp[:n])

	// Entry counter.
	buf.WriteString(`,"`)
	buf.WriteString(jtags['n'].tags[tags])
	buf.WriteString(`":`)
	n = buf.someDigits(0, int(entry.counter))
	buf.Write(buf.tmp[:n])

	// Whether the tags/message are redactable.
	// We use 0/1 instead of true/false, because
	// it's likely there will be more redaction formats
	// in the future.
	buf.WriteString(`,"`)
	buf.WriteString(jtags['r'].tags[tags])
	buf.WriteString(`":`)
	if entry.payload.redactable {
		buf.WriteByte('1')
	} else {
		buf.WriteByte('0')
	}

	// Tags.
	if entry.tags != nil {
		buf.WriteString(`,"tags":{`)
		comma := `"`
		for _, t := range entry.tags.Get() {
			buf.WriteString(comma)
			escapeString(buf, t.Key())
			buf.WriteString(`":"`)
			if v := t.Value(); v != nil && v != "" {
				var r string
				if entry.payload.redactable {
					r = string(redact.Sprint(v))
				} else {
					r = fmt.Sprint(v)
				}
				escapeString(buf, r)
			}
			buf.WriteByte('"')
			comma = `,"`
		}
		buf.WriteByte('}')
	}

	if entry.structured {
		buf.WriteString(`,"event":{`)
		buf.WriteString(entry.payload.message) // Already JSON.
		buf.WriteByte('}')
	} else {
		// Message.
		buf.WriteString(`,"message":"`)
		escapeString(buf, entry.payload.message)
		buf.WriteByte('"')
	}

	// Stacks.
	if len(entry.stacks) > 0 {
		buf.WriteString(`,"stacks":"`)
		escapeString(buf, string(entry.stacks))
		buf.WriteByte('"')
	}
	buf.WriteByte('}')
	buf.WriteByte('\n')
	return buf
}

func escapeString(buf *buffer, s string) {
	b := buf.Bytes()
	b = jsonbytes.EncodeString(b, s)
	buf.Buffer = *bytes.NewBuffer(b)
}
