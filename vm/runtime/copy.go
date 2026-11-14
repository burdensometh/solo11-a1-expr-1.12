package runtime

import (
	"fmt"
	"reflect"
)

// NeedsCopy reports whether values of the given type may contain mutable
// data (slices, maps, pointers, etc.) and therefore should be copied
// before being shared between program runs. Types composed of basic
// immutable values only (numbers, strings, booleans) never need copying.
func NeedsCopy(t reflect.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map, reflect.Interface,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return NeedsCopy(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if NeedsCopy(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

// Copyable returns an error if the given value cannot be safely copied
// with Copy. Values composed of basic types, structs, arrays, slices,
// maps, pointers and interfaces are copyable. Values containing channels,
// functions, unsafe pointers, or unexported struct fields with mutable
// data cannot be copied.
func Copyable(value interface{}) (err error) {
	if value == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	checkCopyable(reflect.ValueOf(value), make(map[copyKey]bool))
	return nil
}

// Copy returns a deep copy of the given value. The copy preserves types,
// shared substructure and cycles: if the same value is referenced more
// than once inside the copied graph, it is copied only once and shared
// in the result as well.
//
// Copy panics on values that cannot be copied; use Copyable to check
// whether a value can be copied beforehand.
func Copy(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	c := &copier{seen: make(map[copyKey]reflect.Value)}
	return c.copy(reflect.ValueOf(value)).Interface()
}

// copyKey identifies a node of a copied value graph. Pointers, slices and
// maps are identified by their type and underlying pointer, so shared and
// cyclic structures are copied only once and keep their identity.
type copyKey struct {
	typ reflect.Type
	ptr uintptr
	len int
	cap int
}

func keyOf(v reflect.Value) copyKey {
	key := copyKey{typ: v.Type(), ptr: v.Pointer()}
	if v.Kind() == reflect.Slice {
		key.len = v.Len()
		key.cap = v.Cap()
	}
	return key
}

func cannotCopy(t reflect.Type) {
	panic(fmt.Errorf("cannot copy value of type %v", t))
}

func cannotCopyField(t reflect.Type, name string) {
	panic(fmt.Errorf("cannot copy value of type %v: unexported field %q", t, name))
}

func checkCopyable(v reflect.Value, visited map[copyKey]bool) {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		key := keyOf(v)
		if visited[key] {
			return
		}
		visited[key] = true
		checkCopyable(v.Elem(), visited)

	case reflect.Slice:
		if v.IsNil() || !NeedsCopy(v.Type().Elem()) {
			return
		}
		key := keyOf(v)
		if visited[key] {
			return
		}
		visited[key] = true
		for i := 0; i < v.Len(); i++ {
			checkCopyable(v.Index(i), visited)
		}

	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := keyOf(v)
		if visited[key] {
			return
		}
		visited[key] = true
		for _, k := range v.MapKeys() {
			checkCopyable(k, visited)
			checkCopyable(v.MapIndex(k), visited)
		}

	case reflect.Interface:
		if !v.IsNil() {
			checkCopyable(v.Elem(), visited)
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !NeedsCopy(f.Type) {
				continue
			}
			if f.PkgPath != "" {
				cannotCopyField(v.Type(), f.Name)
			}
			checkCopyable(v.Field(i), visited)
		}

	case reflect.Array:
		if !NeedsCopy(v.Type().Elem()) {
			return
		}
		for i := 0; i < v.Len(); i++ {
			checkCopyable(v.Index(i), visited)
		}

	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		cannotCopy(v.Type())
	}
}

type copier struct {
	seen map[copyKey]reflect.Value
}

func (c *copier) copy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return v
		}
		key := keyOf(v)
		if prev, ok := c.seen[key]; ok {
			return prev
		}
		out := reflect.New(v.Type().Elem())
		c.seen[key] = out
		out.Elem().Set(c.copy(v.Elem()))
		return out

	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		key := keyOf(v)
		if prev, ok := c.seen[key]; ok {
			return prev
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())
		c.seen[key] = out
		if !NeedsCopy(v.Type().Elem()) {
			reflect.Copy(out, v)
			return out
		}
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copy(v.Index(i)))
		}
		return out

	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := keyOf(v)
		if prev, ok := c.seen[key]; ok {
			return prev
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		c.seen[key] = out
		for _, k := range v.MapKeys() {
			out.SetMapIndex(c.copy(k), c.copy(v.MapIndex(k)))
		}
		return out

	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.copy(v.Elem()))
		return out

	case reflect.Struct:
		if !NeedsCopy(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !NeedsCopy(f.Type) {
				continue
			}
			if f.PkgPath != "" {
				cannotCopyField(v.Type(), f.Name)
			}
			out.Field(i).Set(c.copy(v.Field(i)))
		}
		return out

	case reflect.Array:
		if !NeedsCopy(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copy(v.Index(i)))
		}
		return out

	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		cannotCopy(v.Type())

	}
	return v
}
