package crawlrun

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Every Event field survives a recording. Each field is set to a distinct
// non-zero value by reflection, so a field added to Event without a wire
// counterpart -- or mapped in only one direction -- fails here rather than
// vanishing from every recording, as the credential once did.
func TestWireCarriesEveryEventField(t *testing.T) {
	var ev Event
	v := reflect.ValueOf(&ev).Elem()
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		// Kind is an int underneath, and an arbitrary int is not a kind:
		// it has to be a real one to have a name to round-trip through.
		if f.Type() == reflect.TypeOf(KindUnknown) {
			f.Set(reflect.ValueOf(KindAuthOK))
			continue
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString("value-of-" + name)
		case reflect.Int:
			f.SetInt(int64(100 + i))
		case reflect.Uint64:
			f.SetUint(uint64(200 + i))
		default:
			switch f.Interface().(type) {
			case time.Time:
				f.Set(reflect.ValueOf(time.Date(2026, 9, 10, 20, 14, 47, 561688000, time.UTC)))
			default:
				t.Fatalf("field %s has type %s, which this test does not know how to fill -- extend it", name, f.Type())
			}
		}
	}
	b, err := json.Marshal(toWire(ev))
	if err != nil {
		t.Fatal(err)
	}
	var w wireEvent
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	back := fromWire(w)
	if !back.At.Equal(ev.At) {
		t.Fatalf("At: %v != %v", back.At, ev.At)
	}
	back.At, ev.At = time.Time{}, time.Time{}
	if !reflect.DeepEqual(back, ev) {
		got, want := reflect.ValueOf(back), reflect.ValueOf(ev)
		for i := 0; i < got.NumField(); i++ {
			if !reflect.DeepEqual(got.Field(i).Interface(), want.Field(i).Interface()) {
				t.Errorf("field %s does not survive the wire: %v, want %v",
					got.Type().Field(i).Name, got.Field(i).Interface(), want.Field(i).Interface())
			}
		}
	}
}
