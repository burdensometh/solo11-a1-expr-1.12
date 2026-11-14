package optimizer

import (
	. "github.com/antonmedv/expr/ast"
)

type constRange struct{}

func (*constRange) Visit(node *Node) {
	switch n := (*node).(type) {
	case *BinaryNode:
		if n.Operator == ".." {
			if min, ok := n.Left.(*IntegerNode); ok {
				if max, ok := n.Right.(*IntegerNode); ok {
					// In case the max < min, patch empty slice
					// as max must be greater than equal to min.
					if max.Value < min.Value {
						Patch(node, &ConstantNode{
							Value: make([]int, 0),
						})
						return
					}
					// The size of the range is computed as uint64 to
					// avoid an integer overflow: as max >= min, the
					// difference always fits into uint64.
					size := uint64(max.Value) - uint64(min.Value) + 1
					// In this case array is too big. Skip generation,
					// and wait for memory budget detection on runtime.
					if size == 0 || size > 1e6 {
						return
					}
					value := make([]int, int(size))
					for i := range value {
						value[i] = min.Value + i
					}
					Patch(node, &ConstantNode{
						Value: value,
					})
				}
			}
		}
	}
}
