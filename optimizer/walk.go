package optimizer

import (
	. "github.com/antonmedv/expr/ast"
)

// reachabilityAwareVisitor is implemented by optimizer passes which must not
// observe branches which are known to be unreachable at compile time. The
// certain argument tells whether the node is reached on every execution of its
// enclosing expression (true) or only on some execution path (false, for
// example one branch of a conditional with a non-constant condition).
type reachabilityAwareVisitor interface {
	Visit(node *Node, certain bool)
}

// walk traverses the tree the same way ast.Walk does, but it does not enter
// branches which are known to be unreachable at compile time: the right-hand
// side of a constant "and"/"or" and the not selected branch of a constant
// conditional expression. Branches of a conditional with a non-constant
// condition are still traversed, but marked as not certain. This keeps
// constant folding and evaluation of constant expressions in sync with the
// short-circuit evaluation semantics of the virtual machine, so errors (for
// example a division by zero or a failing ConstExpr function) are not
// triggered for code which would never run, and errors which may or may not
// happen are left for the runtime.
func walk(node *Node, v reachabilityAwareVisitor) {
	walkReachable(node, v, true)
}

func walkReachable(node *Node, v reachabilityAwareVisitor, certain bool) {
	switch n := (*node).(type) {
	case *BinaryNode:
		walkReachable(&n.Left, v, certain)

		// Only "and"/"or" short-circuit the evaluation of their right side.
		reachable := true
		rightCertain := certain
		switch n.Operator {
		case "and", "&&", "or", "||":
			if b, ok := n.Left.(*BoolNode); ok {
				if n.Operator == "and" || n.Operator == "&&" {
					// The right side is reached only when the left side is true.
					reachable = b.Value
				} else {
					// The right side is reached only when the left side is false.
					reachable = !b.Value
				}
			} else {
				// A non-constant left side means the right side runs only on
				// some execution paths.
				rightCertain = false
			}
		}
		if reachable {
			walkReachable(&n.Right, v, rightCertain)
		}

	case *ConditionalNode:
		walkReachable(&n.Cond, v, certain)
		switch cond := n.Cond.(type) {
		case *BoolNode:
			// Only the selected branch is reachable, and its reachability is
			// as certain as the condition leading to it.
			if cond.Value {
				walkReachable(&n.Exp1, v, certain)
			} else {
				walkReachable(&n.Exp2, v, certain)
			}
		default:
			// The condition cannot be resolved at compile time, so either
			// branch may run.
			walkReachable(&n.Exp1, v, false)
			walkReachable(&n.Exp2, v, false)
		}

	case *UnaryNode:
		walkReachable(&n.Node, v, certain)
	case *ChainNode:
		walkReachable(&n.Node, v, certain)
	case *MemberNode:
		walkReachable(&n.Node, v, certain)
		walkReachable(&n.Property, v, certain)
	case *SliceNode:
		walkReachable(&n.Node, v, certain)
		if n.From != nil {
			walkReachable(&n.From, v, certain)
		}
		if n.To != nil {
			walkReachable(&n.To, v, certain)
		}
	case *CallNode:
		walkReachable(&n.Callee, v, certain)
		for i := range n.Arguments {
			walkReachable(&n.Arguments[i], v, certain)
		}
	case *BuiltinNode:
		for i := range n.Arguments {
			walkReachable(&n.Arguments[i], v, certain)
		}
	case *ClosureNode:
		walkReachable(&n.Node, v, certain)
	case *ArrayNode:
		for i := range n.Nodes {
			walkReachable(&n.Nodes[i], v, certain)
		}
	case *MapNode:
		for i := range n.Pairs {
			walkReachable(&n.Pairs[i], v, certain)
		}
	case *PairNode:
		walkReachable(&n.Key, v, certain)
		walkReachable(&n.Value, v, certain)
	}

	v.Visit(node, certain)
}
