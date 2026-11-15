package feature

import (
	"github.com/antonmedv/expr/ast"
)

// analyze walks the AST of a feature expression and classifies every name it
// references into input fields and feature references. It returns the sorted
// lists of direct feature dependencies and referenced input fields.
//
// The classification mirrors the resolution order of the expr checker:
//
//   - the env keyword and names of functions (builtins and functions added
//     with expr.Function) are not references;
//   - names declared by the environment are input fields — an input field
//     never creates a dependency, even if a feature shares its name;
//   - names of other features of the set are feature references and become
//     dependency edges;
//   - any other name is an undeclared input field (only usable when the
//     environment is not strict).
//
// Closure locals are not identifiers in expr: the closure pointer (# and
// #.Field) is a PointerNode, so it is never mistaken for a reference, while
// names from the enclosing scope used inside a closure still classify as
// inputs or feature references.
func analyze(node ast.Node, inputs, features, functions map[string]bool) (deps, inputFields []string) {
	c := &classifier{
		inputs:    inputs,
		features:  features,
		functions: functions,
		deps:      map[string]bool{},
		fields:    map[string]bool{},
	}
	c.walk(node)
	return sortedKeys(c.deps), sortedKeys(c.fields)
}

type classifier struct {
	inputs    map[string]bool
	features  map[string]bool
	functions map[string]bool
	deps      map[string]bool
	fields    map[string]bool
}

func (c *classifier) ident(name string) {
	switch {
	case name == "env":
		// The env keyword refers to the whole environment.
	case c.functions[name]:
		// A builtin or a function added with expr.Function.
	case c.inputs[name]:
		// A declared input field; never a dependency, even if a feature
		// shares the name.
		c.fields[name] = true
	case c.features[name]:
		c.deps[name] = true
	default:
		// An undeclared name can only come from the environment at run
		// time (allowed when the environment is not strict).
		c.fields[name] = true
	}
}

func (c *classifier) walk(node ast.Node) {
	switch n := node.(type) {
	case *ast.IdentifierNode:
		c.ident(n.Value)
	case *ast.UnaryNode:
		c.walk(n.Node)
	case *ast.BinaryNode:
		c.walk(n.Left)
		c.walk(n.Right)
	case *ast.ChainNode:
		c.walk(n.Node)
	case *ast.MemberNode:
		// The property of a.b is a StringNode (a name, not a reference),
		// while the property of a[b] is an expression and is walked.
		if id, ok := n.Node.(*ast.IdentifierNode); ok && id.Value == "env" {
			// env.field is equivalent to a top-level reference to field:
			// features are evaluated against the environment, so env.x
			// reads the input field or the feature value named x.
			if prop, ok := n.Property.(*ast.StringNode); ok {
				c.ident(prop.Value)
			} else {
				c.walk(n.Property)
			}
		} else {
			c.walk(n.Node)
			c.walk(n.Property)
		}
	case *ast.SliceNode:
		c.walk(n.Node)
		c.walk(n.From)
		c.walk(n.To)
	case *ast.CallNode:
		c.walk(n.Callee)
		for _, arg := range n.Arguments {
			c.walk(arg)
		}
	case *ast.BuiltinNode:
		for _, arg := range n.Arguments {
			c.walk(arg)
		}
	case *ast.ClosureNode:
		// Names inside a closure still reference the enclosing scope;
		// only the closure pointer (#) is local, and it is a PointerNode,
		// never an IdentifierNode.
		c.walk(n.Node)
	case *ast.ConditionalNode:
		c.walk(n.Cond)
		c.walk(n.Exp1)
		c.walk(n.Exp2)
	case *ast.ArrayNode:
		for _, item := range n.Nodes {
			c.walk(item)
		}
	case *ast.MapNode:
		for _, pair := range n.Pairs {
			c.walk(pair)
		}
	case *ast.PairNode:
		// An identifier map key ({key: value}) is parsed as a StringNode,
		// so only computed keys like {(key): value} classify as references.
		c.walk(n.Key)
		c.walk(n.Value)
	default:
		// Literals (nil, bool, number, string, constant) and the closure
		// pointer (PointerNode) reference nothing.
	}
}
