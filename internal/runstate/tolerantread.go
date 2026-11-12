package runstate

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// A durable record outlives the binary that wrote it, and this binary is rebuilt
// many times a day. A landing that adds one field to a record therefore leaves
// every process still on the previous build reading records whose shape it has
// never seen — and refusing those records is what wedged three long-lived
// readers in three days: the dashboard thirty-one hours stale on
// work_item_labels, the Slack sink twenty hours on the same field, and the sink
// again on stale_block_clear, that last one retrying the identical pass a
// hundred and nineteen times with its cursors where they were.
//
// So a durable record in this package is read through one of two doors, and
// which door a caller goes through says what the caller is about to do rather
// than how careful it feels like being:
//
//   - A read that only says what a record holds reads tolerantly: a listing, and
//     a surface that fetches one record by id to show it — Store.Read,
//     ConversationStore.Read, ExchangeStore.Read, and the records nothing ever
//     reads to write back, the supervisor's and the watch holder's. It keeps
//     every field this build knows, steps over the fields it does not, and says
//     once for the life of the process — rather than once per pass — which
//     fields it stepped over. What makes that safe here and nowhere else is that
//     nothing decided from such a read is written back: every caller that acts
//     on one re-reads the record through the strict door first, under the
//     record's own lease, because what it read is a snapshot another process may
//     already have moved on from.
//   - Everything that precedes a write reads strictly. A field this build does
//     not know means the record was written by different code, and decoding it as
//     though the field were not there and then saving it back is exactly how the
//     field is lost. That refusal is an error the caller reports on its own
//     surface; it is never an empty answer. So does a gate that declines to
//     proceed on a record it can only read part of — a hold, a pausing directive
//     — whose refusal is the visible failure it is owed.
//
// The same split holds for a value rather than a field. A closed set the record
// draws from — a stop class, a landing outcome — grows when a newer build learns
// a value, and an older build's validation refuses it. Store.Read keeps a run
// record whose only problems are such values, leaves each as written, and lists
// it in State.UnknownValues for the surfaces to show as unknown; Store.Load, and
// every write, still refuse it, naming the value.
//
// decodeStrictly is the second door and decodeTolerating is the first.
// strictdecode_audit_test.go lists every strict read in the tree and which of
// those it is, and fails on one it does not list.

// decodeStrictly decodes one durable record, refusing a record that carries
// anything this build does not know and a record with more than one value in it.
func decodeStrictly(encoded []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

// decodeTolerating decodes one durable record, keeping the fields this build
// knows and naming the ones it does not. Everything a strict decode would refuse
// for some other reason it refuses too: a tolerated field is the one thing this
// steps over.
//
// The strict decode comes first and is nearly always the whole of it, because a
// record this build wrote carries nothing this build does not know. Only a
// record that has something extra in it is read a second time, so a listing over
// records of this build's own shape costs exactly what it cost before. The
// target goes back to its zero value in between rather than being decoded on top
// of whatever the refused pass had already filled in.
func decodeTolerating(encoded []byte, value any) ([]string, error) {
	if err := decodeStrictly(encoded, value); err == nil {
		return nil, nil
	} else if !refusedAnUnknownField(err) {
		return nil, err
	}
	target := reflect.ValueOf(value).Elem()
	target.Set(reflect.Zero(target.Type()))
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(value); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return unknownFields(encoded, target.Type()), nil
}

// refusedAnUnknownField says whether a strict decode failed because the record
// carries a field the type does not have, rather than because the record is
// malformed or holds the wrong sort of value. encoding/json reports it as a
// plain error with no type to match on, so the message is what there is to
// match.
const unknownFieldRefusal = "json: unknown field "

func refusedAnUnknownField(err error) bool {
	return err != nil && strings.Contains(err.Error(), unknownFieldRefusal)
}

// unknownFields names every field the record carries that the type does not
// have, as a dotted path from the record's root.
//
// It walks the record a second time rather than reading the strict decoder's
// error because that error names one field however many the record carries: an
// operator told about one of three new fields has been told the least useful
// part of it, and the two fields it left out never get named at all — the
// message is identical on every pass, so the notice about them is deduplicated
// away.
func unknownFields(encoded []byte, target reflect.Type) []string {
	found := map[string]struct{}{}
	collectUnknownFields(encoded, target, "", found)
	named := make([]string, 0, len(found))
	for path := range found {
		named = append(named, path)
	}
	sort.Strings(named)
	return named
}

func collectUnknownFields(encoded json.RawMessage, target reflect.Type, path string, found map[string]struct{}) {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	// A type that decodes itself says nothing about its own fields, and neither
	// does an empty interface: what is inside one is data rather than a shape this
	// build could be missing part of.
	if decodesItself(target) || target.Kind() == reflect.Interface {
		return
	}
	switch target.Kind() {
	case reflect.Struct:
		fields := map[string]reflect.Type{}
		collectFieldTypes(target, fields)
		var carried map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &carried); err != nil {
			return
		}
		for name, raw := range carried {
			field, known := fields[name]
			if !known {
				found[path+name] = struct{}{}
				continue
			}
			collectUnknownFields(raw, field, path+name+".", found)
		}
	case reflect.Slice, reflect.Array:
		var carried []json.RawMessage
		if err := json.Unmarshal(encoded, &carried); err != nil {
			return
		}
		// Every element is walked and they share one path: a field added to the
		// element type is a field on all of them, and naming it once is the notice
		// somebody can read.
		for _, raw := range carried {
			collectUnknownFields(raw, target.Elem(), path+"[].", found)
		}
	case reflect.Map:
		var carried map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &carried); err != nil {
			return
		}
		// A map's keys are data rather than fields, so the path says which map
		// rather than which entry.
		for _, raw := range carried {
			collectUnknownFields(raw, target.Elem(), path+"[].", found)
		}
	}
}

// collectFieldTypes maps the names one struct's fields are written under to the
// types they are written from, which is what a record's own keys are compared
// against.
func collectFieldTypes(target reflect.Type, into map[string]reflect.Type) {
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		// An embedded struct with no name of its own writes its fields at this
		// level, which is where the record carries them.
		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct && !decodesItself(field.Type) {
				collectFieldTypes(embedded, into)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		into[name] = field.Type
	}
}

var (
	jsonUnmarshaler = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

func decodesItself(target reflect.Type) bool {
	if target.Implements(jsonUnmarshaler) || target.Implements(textUnmarshaler) {
		return true
	}
	pointer := reflect.PointerTo(target)
	return pointer.Implements(jsonUnmarshaler) || pointer.Implements(textUnmarshaler)
}

// unknownFieldNotices is where a listing says what it stepped over, and the
// record of what it has already said.
//
// The record of what has been said is here rather than on a store because a
// surface opens a fresh store for every pass it makes: a notice held per store
// would be said once per pass, which is the thing this exists to stop. Standard
// error is where it goes for the reason the worktree manager's notes go there —
// it is what somebody reads afterwards rather than part of any command's answer,
// and a command asked for JSON has to stay parseable.
var unknownFieldNotices = struct {
	sync.Mutex
	said map[string]bool
	to   io.Writer
}{said: map[string]bool{}, to: os.Stderr}

// noteUnknownFields says once, for the life of this process, that a record of
// one kind carried fields this build does not know and was read without them.
// Saying it again on the next pass would be a line a minute for as long as the
// process runs, which is how a genuinely useful notice becomes the thing an
// operator filters out.
func noteUnknownFields(record string, fields []string) {
	if len(fields) == 0 {
		return
	}
	notice := fmt.Sprintf("this build does not know %s in a %s, and read the rest of the record without them; the record was written by a different build, so restarting this process on that build is what picks them up",
		strings.Join(fields, ", "), record)
	unknownFieldNotices.Lock()
	defer unknownFieldNotices.Unlock()
	if unknownFieldNotices.said[notice] {
		return
	}
	unknownFieldNotices.said[notice] = true
	fmt.Fprintln(unknownFieldNotices.to, notice)
}

// noteUnknownValues says once, for the life of this process, that a record of
// one kind held values this build does not know and was kept with them marked
// unknown, for the reason noteUnknownFields says its notice once.
func noteUnknownValues(record string, values []UnknownValueError) {
	if len(values) == 0 {
		return
	}
	named := make([]string, 0, len(values))
	for _, value := range values {
		named = append(named, fmt.Sprintf("%s %q", value.Field, value.Value))
	}
	sort.Strings(named)
	notice := fmt.Sprintf("this build does not know %s in a %s, and shows the record with those values as unknown; the record was written by a different build, so restarting this process on that build is what reads them",
		strings.Join(named, ", "), record)
	unknownFieldNotices.Lock()
	defer unknownFieldNotices.Unlock()
	if unknownFieldNotices.said[notice] {
		return
	}
	unknownFieldNotices.said[notice] = true
	fmt.Fprintln(unknownFieldNotices.to, notice)
}
