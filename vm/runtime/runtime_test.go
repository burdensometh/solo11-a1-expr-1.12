package runtime_test

import (
	"math"
	"testing"

	"github.com/antonmedv/expr/vm/runtime"
	"github.com/stretchr/testify/assert"
)

func TestRange_Contains(t *testing.T) {
	rng := runtime.Range{Min: 1, Max: 3}

	assert.True(t, rng.Contains(1))
	assert.True(t, rng.Contains(2))
	assert.True(t, rng.Contains(3))
	assert.False(t, rng.Contains(0))
	assert.False(t, rng.Contains(4))

	assert.True(t, rng.Contains(int8(2)))
	assert.True(t, rng.Contains(int16(2)))
	assert.True(t, rng.Contains(int32(2)))
	assert.True(t, rng.Contains(int64(2)))
	assert.True(t, rng.Contains(uint(2)))
	assert.True(t, rng.Contains(uint8(2)))
	assert.True(t, rng.Contains(uint16(2)))
	assert.True(t, rng.Contains(uint32(2)))
	assert.True(t, rng.Contains(uint64(2)))
	assert.False(t, rng.Contains(uint64(4)))

	// A float may hit only if it is an integer itself, not merely
	// because it falls within the boundaries.
	assert.True(t, rng.Contains(2.0))
	assert.True(t, rng.Contains(float32(2.0)))
	assert.False(t, rng.Contains(1.5))
	assert.False(t, rng.Contains(2.5))
	assert.False(t, rng.Contains(3.5))

	// Non-numbers never hit.
	assert.False(t, rng.Contains("2"))
	assert.False(t, rng.Contains("a"))
	assert.False(t, rng.Contains(true))
	assert.False(t, rng.Contains(nil))
	assert.False(t, rng.Contains([]int{2}))
}

func TestRange_Contains_reversed(t *testing.T) {
	// An empty reversed range contains nothing.
	rng := runtime.Range{Min: 3, Max: 1}
	assert.False(t, rng.Contains(1))
	assert.False(t, rng.Contains(2))
	assert.False(t, rng.Contains(3))
	assert.False(t, rng.Contains(2.0))
}

func TestRange_Contains_negative(t *testing.T) {
	rng := runtime.Range{Min: -5, Max: -1}
	assert.True(t, rng.Contains(-5))
	assert.True(t, rng.Contains(-3))
	assert.True(t, rng.Contains(-1))
	assert.False(t, rng.Contains(-6))
	assert.False(t, rng.Contains(0))
}

func TestRange_Contains_int64_boundaries(t *testing.T) {
	rng := runtime.Range{Min: math.MinInt64, Max: math.MaxInt64}
	assert.True(t, rng.Contains(math.MinInt64))
	assert.True(t, rng.Contains(math.MaxInt64))
	assert.True(t, rng.Contains(0))
	assert.True(t, rng.Contains(int64(math.MinInt64)))
	assert.True(t, rng.Contains(int64(math.MaxInt64)))
	assert.True(t, rng.Contains(float64(math.MinInt64))) // exactly -2^63

	// A float beyond the int64 boundaries is not a member, even though
	// every int64 is a member of the range: the comparison is exact
	// and does not approximate the needle as a float.
	assert.False(t, rng.Contains(9223372036854775808.0))  // 2^63
	assert.False(t, rng.Contains(-9223372036854777856.0)) // < -2^63

	tail := runtime.Range{Min: math.MaxInt64 - 2, Max: math.MaxInt64}
	assert.True(t, tail.Contains(math.MaxInt64))
	assert.False(t, tail.Contains(9223372036854775808.0)) // 2^63 > MaxInt64

	head := runtime.Range{Min: math.MinInt64, Max: math.MinInt64 + 2}
	assert.True(t, head.Contains(math.MinInt64))
	assert.False(t, head.Contains(-9223372036854777856.0)) // < -2^63
}

func TestRange_Contains_consistency(t *testing.T) {
	// The boundary check must behave exactly like the membership check
	// over the materialized slice of the range.
	needles := []interface{}{
		0, 1, 2, 3, 4, -1, -5,
		int8(2), int16(2), int32(2), int64(2), int64(-1),
		uint(2), uint8(2), uint16(2), uint32(2), uint64(2),
		uint64(math.MaxUint64),
		0.0, 1.0, 1.5, 2.0, 2.5, 3.0, 4.0, -1.0,
		float32(2.0), float32(1.5),
		"1", "a", "", true, false, nil,
	}
	ranges := []runtime.Range{
		{Min: 1, Max: 3},
		{Min: -5, Max: -1},
		{Min: -1, Max: 1},
		{Min: 3, Max: 1}, // empty reversed range
		{Min: 0, Max: 0},
		{Min: -1, Max: -1},
	}
	for _, rng := range ranges {
		slice := runtime.MakeRange(rng.Min, rng.Max)
		for _, needle := range needles {
			assert.Equal(t, runtime.In(needle, slice), rng.Contains(needle),
				"needle %v (%T) in %v", needle, needle, rng)
		}
	}
}

func TestIn_range(t *testing.T) {
	assert.True(t, runtime.In(2, runtime.Range{Min: 1, Max: 3}))
	assert.False(t, runtime.In(5, runtime.Range{Min: 1, Max: 3}))
	assert.False(t, runtime.In("a", runtime.Range{Min: 1, Max: 3}))
}

func TestMakeRange(t *testing.T) {
	assert.Equal(t, []int{1, 2, 3}, runtime.MakeRange(1, 3))
	assert.Equal(t, []int{-1, 0, 1}, runtime.MakeRange(-1, 1))
	assert.Equal(t, []int{0}, runtime.MakeRange(0, 0))
	assert.Equal(t, []int{math.MaxInt64}, runtime.MakeRange(math.MaxInt64, math.MaxInt64))
	assert.Equal(t, []int{math.MinInt64}, runtime.MakeRange(math.MinInt64, math.MinInt64))

	// Empty reversed ranges.
	assert.Equal(t, []int{}, runtime.MakeRange(3, 1))
	assert.Equal(t, []int{}, runtime.MakeRange(0, -1))

	// Reversed range with extreme boundaries: the size calculation
	// overflows and must not produce a bogus slice.
	assert.Equal(t, []int{}, runtime.MakeRange(math.MaxInt64, math.MinInt64))

	// The full int64 range cannot be allocated.
	assert.Equal(t, []int{}, runtime.MakeRange(math.MinInt64, math.MaxInt64))
}
