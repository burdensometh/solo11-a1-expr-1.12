package optimizer

import (
	. "github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/vm/runtime"
)

type inRange struct{}

func (*inRange) Visit(node *Node) {
	switch n := (*node).(type) {
	case *BinaryNode:
		if n.Operator == "in" {
			if rng, ok := n.Right.(*BinaryNode); ok && rng.Operator == ".." {
				if from, ok := rng.Left.(*IntegerNode); ok {
					if to, ok := rng.Right.(*IntegerNode); ok {
						// Replace the range with a constant, so the left
						// side of "in" is evaluated only once and the
						// membership check is performed with boundary
						// comparisons at runtime (see runtime.Range).
						constant := &ConstantNode{
							Value: runtime.Range{Min: from.Value, Max: to.Value},
						}
						constant.SetLocation(rng.Location())
						Patch(node, &BinaryNode{
							Operator: "in",
							Left:     n.Left,
							Right:    constant,
						})
					}
				}
			}
		}
	}
}
