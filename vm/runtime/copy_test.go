package runtime_test

import (
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/antonmedv/expr/vm/runtime"
	"github.com/stretchr/testify/require"
)

// ptrOf returns the underlying pointer of a map, slice or pointer value.
func ptrOf(v interface{}) uintptr {
	return reflect.ValueOf(v).Pointer()
}

func TestCopy_scalars(t *testing.T) {
	require.Nil(t, runtime.Copy(nil))
	require.Equal(t, 42, runtime.Copy(42))
	require.Equal(t, "foo", runtime.Copy("foo"))
	require.Equal(t, true, runtime.Copy(true))
	require.Equal(t, 1.5, runtime.Copy(1.5))
}

func TestCopy_slice(t *testing.T) {
	original := []int{1, 2, 3}
	copied := runtime.Copy(original).([]int)

	require.Equal(t, original, copied)
	require.NotEqual(t, ptrOf(original), ptrOf(copied))

	copied[0] = 42
	require.Equal(t, []int{1, 2, 3}, original)
}

func TestCopy_slice_of_interfaces(t *testing.T) {
	original := []interface{}{1, "two", []interface{}{3}}
	copied := runtime.Copy(original).([]interface{})

	require.Equal(t, original, copied)

	copied[2].([]interface{})[0] = 42
	require.Equal(t, []interface{}{1, "two", []interface{}{3}}, original)
}

func TestCopy_map(t *testing.T) {
	original := map[string]interface{}{
		"nested": map[string]interface{}{"a": 1},
	}
	copied := runtime.Copy(original).(map[string]interface{})

	require.Equal(t, original, copied)

	copied["nested"].(map[string]interface{})["a"] = 42
	require.Equal(t, 1, original["nested"].(map[string]interface{})["a"])
}

func TestCopy_map_with_interface_keys(t *testing.T) {
	original := map[interface{}]interface{}{"a": 1, 2: "b"}
	copied := runtime.Copy(original).(map[interface{}]interface{})
	require.Equal(t, original, copied)
}

func TestCopy_preserves_shared_substructure(t *testing.T) {
	shared := []int{1, 2, 3}
	original := map[string]interface{}{
		"a": shared,
		"b": shared,
	}
	copied := runtime.Copy(original).(map[string]interface{})

	a := copied["a"].([]int)
	b := copied["b"].([]int)
	require.Equal(t, shared, a)
	require.Equal(t, shared, b)

	// Both references must point to the same copy.
	require.Equal(t, ptrOf(a), ptrOf(b))
	a[0] = 42
	require.Equal(t, 42, b[0])
	// The original constant must stay intact.
	require.Equal(t, []int{1, 2, 3}, shared)
}

func TestCopy_preserves_cycles(t *testing.T) {
	original := map[string]interface{}{}
	original["self"] = original

	copied := runtime.Copy(original).(map[string]interface{})
	self, ok := copied["self"].(map[string]interface{})
	require.True(t, ok)
	require.NotEqual(t, ptrOf(original), ptrOf(copied))
	// The cycle must point to the copy, not to the original.
	require.Equal(t, ptrOf(copied), ptrOf(self))
}

func TestCopy_preserves_slice_cycle(t *testing.T) {
	original := make([]interface{}, 1)
	original[0] = original

	copied := runtime.Copy(original).([]interface{})
	require.NotEqual(t, ptrOf(original), ptrOf(copied))
	require.Equal(t, ptrOf(copied), ptrOf(copied[0].([]interface{})))
}

type copyNode struct {
	Value int
	Next  *copyNode
}

func TestCopy_preserves_pointer_cycle(t *testing.T) {
	original := &copyNode{Value: 1}
	original.Next = original

	copied := runtime.Copy(original).(*copyNode)
	require.NotSame(t, original, copied)
	require.Same(t, copied, copied.Next)
	require.Equal(t, 1, copied.Value)
}

func TestCopy_pointer(t *testing.T) {
	original := &copyNode{Value: 1, Next: &copyNode{Value: 2}}
	copied := runtime.Copy(original).(*copyNode)

	require.NotSame(t, original, copied)
	require.NotSame(t, original.Next, copied.Next)
	require.Equal(t, original, copied)

	copied.Next.Value = 42
	require.Equal(t, 2, original.Next.Value)
}

type copyStruct struct {
	Name   string
	Values []int
}

func TestCopy_struct(t *testing.T) {
	original := copyStruct{Name: "test", Values: []int{1, 2}}
	copied := runtime.Copy(original).(copyStruct)

	require.Equal(t, original, copied)

	copied.Values[0] = 42
	require.Equal(t, 1, original.Values[0])
}

func TestCopy_array(t *testing.T) {
	original := [2][]int{{1}, {2}}
	copied := runtime.Copy(original).([2][]int)

	require.Equal(t, original, copied)

	copied[0][0] = 42
	require.Equal(t, 1, original[0][0])
}

func TestCopy_nil_values(t *testing.T) {
	require.Equal(t, []int(nil), runtime.Copy([]int(nil)).([]int))
	require.Equal(t, map[string]int(nil), runtime.Copy(map[string]int(nil)).(map[string]int))
	require.Equal(t, (*int)(nil), runtime.Copy((*int)(nil)).(*int))

	original := []interface{}{nil, map[string]interface{}{"a": nil}}
	copied := runtime.Copy(original).([]interface{})
	require.Equal(t, original, copied)
}

func TestCopy_unsupported(t *testing.T) {
	require.Panics(t, func() {
		runtime.Copy(make(chan int))
	})
	require.Panics(t, func() {
		runtime.Copy(func() {})
	})
	require.Panics(t, func() {
		runtime.Copy(time.Now())
	})
}

func TestCopyable(t *testing.T) {
	require.NoError(t, runtime.Copyable(nil))
	require.NoError(t, runtime.Copyable(42))
	require.NoError(t, runtime.Copyable("foo"))
	require.NoError(t, runtime.Copyable([]int{1, 2, 3}))
	require.NoError(t, runtime.Copyable([]interface{}{1, "a", []float64{1.5}}))
	require.NoError(t, runtime.Copyable(map[string]interface{}{"a": []int{1}}))
	require.NoError(t, runtime.Copyable(&copyNode{Value: 1}))
	require.NoError(t, runtime.Copyable(copyStruct{}))

	// Cyclic values are copyable.
	cyclic := map[string]interface{}{}
	cyclic["self"] = cyclic
	require.NoError(t, runtime.Copyable(cyclic))
}

func TestCopyable_unsupported(t *testing.T) {
	require.EqualError(t, runtime.Copyable(make(chan int)), "cannot copy value of type chan int")
	require.EqualError(t, runtime.Copyable(func() {}), "cannot copy value of type func()")
	require.EqualError(t, runtime.Copyable(unsafe.Pointer(nil)), "cannot copy value of type unsafe.Pointer")
	require.EqualError(t, runtime.Copyable([]interface{}{make(chan int)}), "cannot copy value of type chan int")
	require.EqualError(t, runtime.Copyable(map[string]func(){"a": func() {}}), "cannot copy value of type func()")

	// Structs with unexported fields holding mutable data cannot be copied.
	err := runtime.Copyable(time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot copy value of type time.Time: unexported field")
}

func TestNeedsCopy(t *testing.T) {
	require.False(t, runtime.NeedsCopy(nil))
	require.False(t, runtime.NeedsCopy(reflect.TypeOf(0)))
	require.False(t, runtime.NeedsCopy(reflect.TypeOf("")))
	require.False(t, runtime.NeedsCopy(reflect.TypeOf(false)))
	require.False(t, runtime.NeedsCopy(reflect.TypeOf(0.5)))
	require.False(t, runtime.NeedsCopy(reflect.TypeOf([2]int{})))

	require.True(t, runtime.NeedsCopy(reflect.TypeOf([]int{})))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf(map[string]int{})))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf(&copyNode{})))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf(copyStruct{})))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf([2][]int{})))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf(make(chan int))))
	require.True(t, runtime.NeedsCopy(reflect.TypeOf(func() {})))
}
