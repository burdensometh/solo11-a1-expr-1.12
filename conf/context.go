package conf

import (
	"context"
	"reflect"

	"github.com/antonmedv/expr/ast"
)

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// ContextPatcher inserts a ContextNode as the first argument of every host
// function which accepts a context.Context. The node is resolved to the
// context of the current run at evaluation time; the compiled program stores
// no context value, so one program can serve many jobs.
type ContextPatcher struct {
	// Config is the configuration being compiled. It is consulted lazily, so
	// the context option may be passed before or after the option declaring
	// the environment and the available functions.
	Config *Config
}

func (p *ContextPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	// The patcher may run once per visitor pass; do not add the context twice.
	if len(call.Arguments) > 0 {
		if _, ok := call.Arguments[0].(*ast.ContextNode); ok {
			return
		}
	}
	fn := p.funcType(call)
	if fn == nil || fn.Kind() != reflect.Func || fn.NumIn() == 0 {
		return
	}
	if !acceptsContext(call, fn) {
		return
	}
	call.Arguments = append([]ast.Node{&ast.ContextNode{}}, call.Arguments...)
}

// acceptsContext reports whether fn takes the job's context. For a method the
// receiver occupies the first input, so the context may be the second one;
// the receiver itself is bound by the caller and is not an argument.
func acceptsContext(call *ast.CallNode, fn reflect.Type) bool {
	if fn.In(0) == contextType {
		return true
	}
	if fn.NumIn() > 1 && fn.In(1) == contextType && isMethod(call.Callee) {
		return true
	}
	return false
}

func isMethod(callee ast.Node) bool {
	switch n := callee.(type) {
	case *ast.IdentifierNode:
		return n.Method
	case *ast.MemberNode:
		return n.Method
	}
	return false
}

// funcType resolves the type of the called function. Named functions declared
// through env or Function options may be typed as interface{} by the checker,
// so their declared signature is looked up in the configuration.
func (p *ContextPatcher) funcType(call *ast.CallNode) reflect.Type {
	if t := call.Callee.Type(); t != nil && t.Kind() == reflect.Func {
		return t
	}
	if p == nil || p.Config == nil {
		return nil
	}
	if call.Func != nil {
		for _, t := range call.Func.Types {
			if t != nil && t.Kind() == reflect.Func {
				return t
			}
		}
	}
	ident, ok := call.Callee.(*ast.IdentifierNode)
	if !ok {
		return nil
	}
	if fn, ok := p.Config.Functions[ident.Value]; ok {
		for _, t := range fn.Types {
			if t != nil && t.Kind() == reflect.Func {
				return t
			}
		}
	}
	if tag, ok := p.Config.Types[ident.Value]; ok && tag.Type != nil && tag.Type.Kind() == reflect.Func {
		return tag.Type
	}
	return nil
}
