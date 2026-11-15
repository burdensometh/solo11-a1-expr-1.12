package feature

import (
	"reflect"
)

// toMap views an environment value as a map of input fields, so that user
// inputs and computed feature values can be supplied to the evaluator in a
// single map. It mirrors the field and method exposure of
// conf.CreateTypesTable:
//
//   - a map contributes its string-keyed entries (the map is copied, never
//     mutated) and its methods;
//   - a struct (or a pointer to one) contributes its exported fields —
//     flattened for embedded structs, honoring the `expr` tag — and its
//     methods as bound functions;
//   - anything else contributes nothing.
//
// Names that would be ambiguous in the types table (promoted by more than
// one embedded struct) are omitted.
func toMap(env interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	if env == nil {
		return out
	}

	v := reflect.ValueOf(env)
	t := v.Type()

	d := v
	for d.Kind() == reflect.Ptr {
		if d.IsNil() {
			d = reflect.Value{}
			break
		}
		d = d.Elem()
	}
	if !d.IsValid() {
		return out
	}

	switch d.Kind() {
	case reflect.Map:
		if d.Type().Key().Kind() == reflect.String {
			iter := d.MapRange()
			for iter.Next() {
				out[iter.Key().String()] = iter.Value().Interface()
			}
		}
		// A map may have methods too.
		for i := 0; i < t.NumMethod(); i++ {
			out[t.Method(i).Name] = v.Method(i).Interface()
		}

	case reflect.Struct:
		fields, ambiguous := structFields(d)
		for name, value := range fields {
			if !ambiguous[name] {
				out[name] = value
			}
		}
		// Methods are gathered from the original value, as in
		// conf.CreateTypesTable, so pointer receiver methods are included
		// when a pointer is passed.
		for i := 0; i < t.NumMethod(); i++ {
			out[t.Method(i).Name] = v.Method(i).Interface()
		}
	}
	return out
}

// structFields flattens the exported fields of a struct value into a name to
// value map, following the promotion rules of conf.FieldsFromStruct: fields
// of embedded structs are promoted, equally named promoted fields are
// ambiguous, and a directly declared field always wins over a promoted one.
// Unexported fields are skipped.
func structFields(v reflect.Value) (fields map[string]interface{}, ambiguous map[string]bool) {
	fields = make(map[string]interface{})
	ambiguous = make(map[string]bool)
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		if f.Anonymous {
			ev := v.Field(i)
			for ev.Kind() == reflect.Ptr {
				if ev.IsNil() {
					ev = reflect.Value{}
					break
				}
				ev = ev.Elem()
			}
			if ev.IsValid() && ev.Kind() == reflect.Struct {
				promoted, promotedAmbiguous := structFields(ev)
				for name := range promotedAmbiguous {
					delete(fields, name)
					ambiguous[name] = true
				}
				for name, value := range promoted {
					if ambiguous[name] {
						continue
					}
					if _, exists := fields[name]; exists {
						delete(fields, name)
						ambiguous[name] = true
						continue
					}
					fields[name] = value
				}
			}
		}

		if f.PkgPath != "" {
			continue // unexported field
		}
		fv := v.Field(i)
		if !fv.CanInterface() {
			continue // not accessible, e.g. through an unexported embedded struct
		}

		name := f.Name
		if tagged := f.Tag.Get("expr"); tagged != "" {
			name = tagged
		}
		// A directly declared field wins over any promoted field.
		delete(ambiguous, name)
		fields[name] = fv.Interface()
	}
	return fields, ambiguous
}
