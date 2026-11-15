package vm

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/builtin"
	"github.com/antonmedv/expr/file"
	"github.com/antonmedv/expr/vm/runtime"
)

type Program struct {
	Node      ast.Node
	Source    *file.Source
	Locations []file.Location
	Constants []interface{}
	Bytecode  []Opcode
	Arguments []int
	Functions []Function

	// ContextName is the environment variable declared by the context option.
	// It is only the name, never a context value: the context itself is
	// resolved per run, so a program can serve many jobs.
	ContextName string

	// constantSizes holds, for every constant, the number of elements it
	// materializes when pushed. It is precomputed so the OpPush hot path is
	// just a slice lookup; it is only read, never modified, so the program
	// stays shareable between jobs.
	constantSizes []int64
}

// ConstantSize returns the work cost of pushing constant index arg. Programs
// built by the compiler carry precomputed sizes; a program assembled by hand
// falls back to measuring the constant on demand.
func (program *Program) ConstantSize(arg int) int64 {
	if arg < 0 || arg >= len(program.Constants) {
		return 0
	}
	if len(program.constantSizes) != len(program.Constants) {
		return materializedSize(program.Constants[arg])
	}
	return program.constantSizes[arg]
}

// SetConstantSizes stores the precomputed sizes of the program constants.
func (program *Program) SetConstantSizes(sizes []int64) {
	program.constantSizes = sizes
}

// MaterializedSizes measures every constant once, at compile time.
func MaterializedSizes(constants []interface{}) []int64 {
	sizes := make([]int64, len(constants))
	for i, c := range constants {
		sizes[i] = materializedSize(c)
	}
	return sizes
}

func (program *Program) Disassemble() string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	ip := 0
	for ip < len(program.Bytecode) {
		pp := ip
		op := program.Bytecode[ip]
		arg := program.Arguments[ip]
		ip += 1

		code := func(label string) {
			_, _ = fmt.Fprintf(w, "%v\t%v\n", pp, label)
		}
		jump := func(label string) {
			_, _ = fmt.Fprintf(w, "%v\t%v\t<%v>\t(%v)\n", pp, label, arg, ip+arg)
		}
		jumpBack := func(label string) {
			_, _ = fmt.Fprintf(w, "%v\t%v\t<%v>\t(%v)\n", pp, label, arg, ip-arg)
		}
		argument := func(label string) {
			_, _ = fmt.Fprintf(w, "%v\t%v\t<%v>\n", pp, label, arg)
		}
		constant := func(label string) {
			var c interface{}
			if arg < len(program.Constants) {
				c = program.Constants[arg]
			} else {
				c = "out of range"
			}
			if r, ok := c.(*regexp.Regexp); ok {
				c = r.String()
			}
			if field, ok := c.(*runtime.Field); ok {
				c = fmt.Sprintf("{%v %v}", strings.Join(field.Path, "."), field.Index)
			}
			if method, ok := c.(*runtime.Method); ok {
				c = fmt.Sprintf("{%v %v}", method.Name, method.Index)
			}
			_, _ = fmt.Fprintf(w, "%v\t%v\t<%v>\t%v\n", pp, label, arg, c)
		}
		builtIn := func(label string) {
			f, ok := builtin.Builtins[arg]
			if !ok {
				panic(fmt.Sprintf("unknown builtin %v", arg))
			}
			_, _ = fmt.Fprintf(w, "%v\t%v\t%v\n", pp, "OpBuiltin", f.Name)
		}

		switch op {
		case OpInvalid:
			code("OpInvalid")

		case OpPush:
			constant("OpPush")

		case OpPushInt:
			argument("OpPushInt")

		case OpPop:
			code("OpPop")

		case OpLoadConst:
			constant("OpLoadConst")

		case OpLoadField:
			constant("OpLoadField")

		case OpLoadFast:
			constant("OpLoadFast")

		case OpLoadMethod:
			constant("OpLoadMethod")

		case OpLoadFunc:
			argument("OpLoadFunc")

		case OpLoadEnv:
			code("OpLoadEnv")

		case OpFetch:
			code("OpFetch")

		case OpFetchField:
			constant("OpFetchField")

		case OpMethod:
			constant("OpMethod")

		case OpTrue:
			code("OpTrue")

		case OpFalse:
			code("OpFalse")

		case OpNil:
			code("OpNil")

		case OpNegate:
			code("OpNegate")

		case OpNot:
			code("OpNot")

		case OpEqual:
			code("OpEqual")

		case OpEqualInt:
			code("OpEqualInt")

		case OpEqualString:
			code("OpEqualString")

		case OpJump:
			jump("OpJump")

		case OpJumpIfTrue:
			jump("OpJumpIfTrue")

		case OpJumpIfFalse:
			jump("OpJumpIfFalse")

		case OpJumpIfNil:
			jump("OpJumpIfNil")

		case OpJumpIfNotNil:
			jump("OpJumpIfNotNil")

		case OpJumpIfEnd:
			jump("OpJumpIfEnd")

		case OpJumpBackward:
			jumpBack("OpJumpBackward")

		case OpIn:
			code("OpIn")

		case OpLess:
			code("OpLess")

		case OpMore:
			code("OpMore")

		case OpLessOrEqual:
			code("OpLessOrEqual")

		case OpMoreOrEqual:
			code("OpMoreOrEqual")

		case OpAdd:
			code("OpAdd")

		case OpSubtract:
			code("OpSubtract")

		case OpMultiply:
			code("OpMultiply")

		case OpDivide:
			code("OpDivide")

		case OpModulo:
			code("OpModulo")

		case OpExponent:
			code("OpExponent")

		case OpRange:
			code("OpRange")

		case OpMatches:
			code("OpMatches")

		case OpMatchesConst:
			constant("OpMatchesConst")

		case OpContains:
			code("OpContains")

		case OpStartsWith:
			code("OpStartsWith")

		case OpEndsWith:
			code("OpEndsWith")

		case OpSlice:
			code("OpSlice")

		case OpCall:
			argument("OpCall")

		case OpCall0:
			argument("OpCall0")

		case OpCall1:
			argument("OpCall1")

		case OpCall2:
			argument("OpCall2")

		case OpCall3:
			argument("OpCall3")

		case OpCallN:
			argument("OpCallN")

		case OpCallFast:
			argument("OpCallFast")

		case OpCallTyped:
			signature := reflect.TypeOf(FuncTypes[arg]).Elem().String()
			_, _ = fmt.Fprintf(w, "%v\t%v\t<%v>\t%v\n", pp, "OpCallTyped", arg, signature)

		case OpBuiltin:
			builtIn("OpBuiltin")

		case OpArray:
			code("OpArray")

		case OpMap:
			code("OpMap")

		case OpLen:
			code("OpLen")

		case OpCast:
			argument("OpCast")

		case OpDeref:
			code("OpDeref")

		case OpIncrementIt:
			code("OpIncrementIt")

		case OpIncrementCount:
			code("OpIncrementCount")

		case OpGetCount:
			code("OpGetCount")

		case OpGetLen:
			code("OpGetLen")

		case OpPointer:
			code("OpPointer")

		case OpLoadContext:
			code("OpLoadContext")

		case OpBegin:
			code("OpBegin")

		case OpEnd:
			code("OpEnd")

		default:
			_, _ = fmt.Fprintf(w, "%v\t%#x (unknown)\n", ip, op)
		}
	}
	_ = w.Flush()
	return buf.String()
}
