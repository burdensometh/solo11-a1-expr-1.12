package vm

//go:generate sh -c "go run ./func_types > ./generated.go"

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/antonmedv/expr/builtin"
	"github.com/antonmedv/expr/file"
	"github.com/antonmedv/expr/vm/runtime"
)

var MemoryBudget int = 1e6
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// ErrWorkBudgetExceeded is matched by errors.Is for the error a run fails
// with when it consumes more work than the job's Limits allow.
var ErrWorkBudgetExceeded = errors.New("work budget exceeded")

// WorkBudgetExceededError reports that a run exceeded the work budget of its
// job. Its message keeps the expression location bound by the VM; errors.Is
// matches ErrWorkBudgetExceeded.
type WorkBudgetExceededError struct {
	Budget int64
	Work   int64
}

func (e *WorkBudgetExceededError) Error() string {
	return fmt.Sprintf("%v (work budget %d)", ErrWorkBudgetExceeded, e.Budget)
}

func (e *WorkBudgetExceededError) Is(target error) bool {
	return target == ErrWorkBudgetExceeded
}

// contextCheckMask makes the VM poll the context every 256 instructions, in
// addition to the checks made at every loop iteration, range construction and
// host function call. It bounds the cancellation latency for straight-line
// code.
const contextCheckMask = 0xff

type Function = func(params ...interface{}) (interface{}, error)

// Limits describes the resources available to a single evaluation.
//
// Limits are supplied per Run and are never stored in a Program, so one
// compiled program may be evaluated by any number of jobs with independent
// cancellation signals and work quotas. Whenever a run exceeds its limits it
// fails with an error bound to the expression location; the program itself is
// left untouched and may be run again.
type Limits struct {
	// Context is the cancellation signal of the job. It is observed while
	// iterating collections (all/any/one/none/filter/map/count), inside
	// nested predicates, while constructing ranges and before calling host
	// functions. Host functions which accept a context.Context as their
	// first parameter receive this context. nil means no cancellation.
	Context context.Context

	// WorkBudget is the maximum amount of work the run may perform, in work
	// units. One unit is one executed bytecode instruction; materializing a
	// range or pushing a collection constant (for example a range folded by
	// the optimizer) additionally costs one unit per element. A value less
	// than or equal to zero means the work is not limited; work is only
	// counted for limited runs, see VM.WorkUsed.
	WorkBudget int64
}

func Run(program *Program, env interface{}) (interface{}, error) {
	if program == nil {
		return nil, fmt.Errorf("program is nil")
	}

	vm := VM{}
	return vm.Run(program, env)
}

// RunWithLimits evaluates the program with the given per-run limits.
func RunWithLimits(program *Program, env interface{}, limits Limits) (interface{}, error) {
	if program == nil {
		return nil, fmt.Errorf("program is nil")
	}

	vm := VM{}
	return vm.RunWithLimits(program, env, limits)
}

type VM struct {
	stack        []interface{}
	ip           int
	scopes       []*Scope
	debug        bool
	step         chan struct{}
	curr         chan int
	memory       int
	memoryBudget int

	ctx        context.Context
	meter      bool
	work       int64
	workBudget int64
	ticks      uint

	// programContextName is set for the duration of a run from the program
	// being evaluated, so context() can fall back to the env's context.
	programContextName string
}

type Scope struct {
	Array reflect.Value
	It    int
	Len   int
	Count int
}

func Debug() *VM {
	vm := &VM{
		debug: true,
		step:  make(chan struct{}, 0),
		curr:  make(chan int, 0),
	}
	return vm
}

func (vm *VM) Run(program *Program, env interface{}) (interface{}, error) {
	return vm.RunWithLimits(program, env, Limits{})
}

// RunWithLimits evaluates the program with the given per-run limits. Limits
// belong to this evaluation only: the program, and therefore any other job
// using it, is never affected by a cancellation or an exceeded quota.
func (vm *VM) RunWithLimits(program *Program, env interface{}, limits Limits) (_ interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			var location file.Location
			if vm.ip >= 1 && vm.ip-1 < len(program.Locations) {
				location = program.Locations[vm.ip-1]
			}
			f := &file.Error{
				Location: location,
				Message:  fmt.Sprintf("%v", r),
			}
			if err, ok := r.(error); ok {
				f.Wrap(err)
			}
			err = f.Bind(program.Source)
		}
	}()

	if vm.stack == nil {
		vm.stack = make([]interface{}, 0, 2)
	} else {
		vm.stack = vm.stack[0:0]
	}

	if vm.scopes != nil {
		vm.scopes = vm.scopes[0:0]
	}

	vm.memoryBudget = MemoryBudget
	vm.memory = 0
	vm.ip = 0

	// Limits live only in this VM instance for the duration of this run,
	// so a failed run never poisons the compiled program for other jobs.
	vm.ctx = limits.Context
	vm.workBudget = limits.WorkBudget
	vm.meter = limits.Context != nil || limits.WorkBudget > 0
	vm.work = 0
	vm.ticks = 0
	vm.programContextName = program.ContextName
	meter := vm.meter

	// A job cancelled before it started does not run at all: report the
	// cancellation rather than whatever limit it might hit first. The error
	// is anchored at the start of the expression.
	if vm.ctx != nil {
		if err := vm.ctx.Err(); err != nil {
			if len(program.Locations) > 0 {
				vm.ip = 1
			}
			panic(err)
		}
	}

	// Metering is exact even on optimized paths: a program whose collections
	// were folded at compile time has no loops to charge, so the constants
	// it pushes are charged by their size instead (see OpPush).

	for vm.ip < len(program.Bytecode) {
		if vm.debug {
			<-vm.step
		}

		op := program.Bytecode[vm.ip]
		arg := program.Arguments[vm.ip]
		vm.ip += 1

		if meter {
			vm.meterInstruction()
		}

		switch op {

		case OpInvalid:
			panic("invalid opcode")

		case OpPush:
			value := program.Constants[arg]
			if meter {
				if n := program.ConstantSize(arg); n > 0 {
					// Constants are metered by their size: a collection
					// folded at compile time (a const range, an array
					// literal, the map of an optimized "in") is pushed as
					// one instruction, so without this charge the optimized
					// path would skip the work its runtime counterpart pays.
					// Materializing a large constant also observes
					// cancellation, just like runtime range construction.
					if vm.ctx != nil {
						vm.checkContext()
					}
					vm.chargeWork(n)
				}
			}
			vm.push(value)

		case OpPop:
			vm.pop()

		case OpLoadConst:
			vm.push(runtime.Fetch(env, program.Constants[arg]))

		case OpLoadField:
			vm.push(runtime.FetchField(env, program.Constants[arg].(*runtime.Field)))

		case OpLoadFast:
			vm.push(env.(map[string]interface{})[program.Constants[arg].(string)])

		case OpLoadMethod:
			vm.push(runtime.FetchMethod(env, program.Constants[arg].(*runtime.Method)))

		case OpLoadFunc:
			vm.push(program.Functions[arg])

		case OpFetch:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Fetch(a, b))

		case OpFetchField:
			a := vm.pop()
			vm.push(runtime.FetchField(a, program.Constants[arg].(*runtime.Field)))

		case OpLoadEnv:
			vm.push(env)

		case OpMethod:
			a := vm.pop()
			vm.push(runtime.FetchMethod(a, program.Constants[arg].(*runtime.Method)))

		case OpTrue:
			vm.push(true)

		case OpFalse:
			vm.push(false)

		case OpNil:
			vm.push(nil)

		case OpNegate:
			v := runtime.Negate(vm.pop())
			vm.push(v)

		case OpNot:
			v := vm.pop().(bool)
			vm.push(!v)

		case OpEqual:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Equal(a, b))

		case OpEqualInt:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.(int) == b.(int))

		case OpEqualString:
			b := vm.pop()
			a := vm.pop()
			vm.push(a.(string) == b.(string))

		case OpJump:
			vm.ip += arg

		case OpJumpIfTrue:
			if vm.current().(bool) {
				vm.ip += arg
			}

		case OpJumpIfFalse:
			if !vm.current().(bool) {
				vm.ip += arg
			}

		case OpJumpIfNil:
			if runtime.IsNil(vm.current()) {
				vm.ip += arg
			}

		case OpJumpIfNotNil:
			if !runtime.IsNil(vm.current()) {
				vm.ip += arg
			}

		case OpJumpIfEnd:
			scope := vm.Scope()
			if scope.It >= scope.Len {
				vm.ip += arg
			} else if vm.ctx != nil {
				// Every collection traversal iteration observes the
				// cancellation signal of the job, nesting included.
				vm.checkContext()
			}

		case OpJumpBackward:
			vm.ip -= arg

		case OpIn:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.In(a, b))

		case OpLess:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Less(a, b))

		case OpMore:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.More(a, b))

		case OpLessOrEqual:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.LessOrEqual(a, b))

		case OpMoreOrEqual:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.MoreOrEqual(a, b))

		case OpAdd:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Add(a, b))

		case OpSubtract:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Subtract(a, b))

		case OpMultiply:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Multiply(a, b))

		case OpDivide:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Divide(a, b))

		case OpModulo:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Modulo(a, b))

		case OpExponent:
			b := vm.pop()
			a := vm.pop()
			vm.push(runtime.Exponent(a, b))

		case OpRange:
			b := vm.pop()
			a := vm.pop()
			if vm.ctx != nil {
				vm.checkContext()
			}
			min := runtime.ToInt(a)
			max := runtime.ToInt(b)
			size := max - min + 1
			if vm.meter && size > 0 {
				// Constructing a range is real work proportional to its
				// length, exactly like the loops over it would be. It is
				// charged before the allocation, so a quota is reported as
				// the reason a huge range is refused when both limits apply.
				vm.chargeWork(int64(size))
			}
			if vm.memory+size >= vm.memoryBudget {
				panic("memory budget exceeded")
			}
			vm.push(runtime.MakeRange(min, max))
			vm.memory += size

		case OpMatches:
			b := vm.pop()
			a := vm.pop()
			match, err := regexp.MatchString(b.(string), a.(string))
			if err != nil {
				panic(err)
			}

			vm.push(match)

		case OpMatchesConst:
			a := vm.pop()
			r := program.Constants[arg].(*regexp.Regexp)
			vm.push(r.MatchString(a.(string)))

		case OpContains:
			b := vm.pop()
			a := vm.pop()
			vm.push(strings.Contains(a.(string), b.(string)))

		case OpStartsWith:
			b := vm.pop()
			a := vm.pop()
			vm.push(strings.HasPrefix(a.(string), b.(string)))

		case OpEndsWith:
			b := vm.pop()
			a := vm.pop()
			vm.push(strings.HasSuffix(a.(string), b.(string)))

		case OpSlice:
			from := vm.pop()
			to := vm.pop()
			node := vm.pop()
			vm.push(runtime.Slice(node, from, to))

		case OpCall:
			fn := reflect.ValueOf(vm.pop())
			size := arg
			in := make([]reflect.Value, size)
			for i := int(size) - 1; i >= 0; i-- {
				param := vm.pop()
				if param == nil && reflect.TypeOf(param) == nil {
					// In case of nil value and nil type use this hack,
					// otherwise reflect.Call will panic on zero value.
					in[i] = reflect.ValueOf(&param).Elem()
				} else {
					in[i] = reflect.ValueOf(param)
				}
			}
			if vm.ctx != nil {
				vm.checkContext()
			}
			out := fn.Call(in)
			if len(out) == 2 && out[1].Type() == errorType && !out[1].IsNil() {
				panic(out[1].Interface().(error))
			}
			vm.push(out[0].Interface())

		case OpCall0:
			vm.checkContext()
			out, err := program.Functions[arg]()
			if err != nil {
				panic(err)
			}
			vm.push(out)

		case OpCall1:
			a := vm.pop()
			vm.checkContext()
			out, err := program.Functions[arg](a)
			if err != nil {
				panic(err)
			}
			vm.push(out)

		case OpCall2:
			b := vm.pop()
			a := vm.pop()
			vm.checkContext()
			out, err := program.Functions[arg](a, b)
			if err != nil {
				panic(err)
			}
			vm.push(out)

		case OpCall3:
			c := vm.pop()
			b := vm.pop()
			a := vm.pop()
			vm.checkContext()
			out, err := program.Functions[arg](a, b, c)
			if err != nil {
				panic(err)
			}
			vm.push(out)

		case OpCallN:
			fn := vm.pop().(Function)
			size := arg
			in := make([]interface{}, size)
			for i := int(size) - 1; i >= 0; i-- {
				in[i] = vm.pop()
			}
			vm.checkContext()
			out, err := fn(in...)
			if err != nil {
				panic(err)
			}
			vm.push(out)

		case OpCallFast:
			fn := vm.pop().(func(...interface{}) interface{})
			size := arg
			in := make([]interface{}, size)
			for i := int(size) - 1; i >= 0; i-- {
				in[i] = vm.pop()
			}
			vm.checkContext()
			vm.push(fn(in...))

		case OpCallTyped:
			fn := vm.pop()
			vm.checkContext()
			out := vm.call(fn, arg)
			vm.push(out)

		case OpArray:
			size := vm.pop().(int)
			array := make([]interface{}, size)
			for i := size - 1; i >= 0; i-- {
				array[i] = vm.pop()
			}
			vm.push(array)
			vm.memory += size
			if vm.memory >= vm.memoryBudget {
				panic("memory budget exceeded")
			}

		case OpMap:
			size := vm.pop().(int)
			m := make(map[string]interface{})
			for i := size - 1; i >= 0; i-- {
				value := vm.pop()
				key := vm.pop()
				m[key.(string)] = value
			}
			vm.push(m)
			vm.memory += size
			if vm.memory >= vm.memoryBudget {
				panic("memory budget exceeded")
			}

		case OpLen:
			vm.push(runtime.Len(vm.current()))

		case OpCast:
			t := arg
			switch t {
			case 0:
				vm.push(runtime.ToInt(vm.pop()))
			case 1:
				vm.push(runtime.ToInt64(vm.pop()))
			case 2:
				vm.push(runtime.ToFloat64(vm.pop()))
			}

		case OpDeref:
			a := vm.pop()
			vm.push(runtime.Deref(a))

		case OpIncrementIt:
			scope := vm.Scope()
			scope.It++

		case OpIncrementCount:
			scope := vm.Scope()
			scope.Count++

		case OpGetCount:
			scope := vm.Scope()
			vm.push(scope.Count)

		case OpGetLen:
			scope := vm.Scope()
			vm.push(scope.Len)

		case OpPointer:
			scope := vm.Scope()
			vm.push(scope.Array.Index(scope.It).Interface())

		case OpLoadContext:
			// The context of this run, never a value stored in the program.
			vm.push(vm.context(env))

		case OpBegin:
			a := vm.pop()
			array := reflect.ValueOf(a)
			vm.scopes = append(vm.scopes, &Scope{
				Array: array,
				Len:   array.Len(),
			})

		case OpEnd:
			vm.scopes = vm.scopes[:len(vm.scopes)-1]

		case OpBuiltin:
			switch arg {
			case builtin.Len:
				vm.push(runtime.Len(vm.pop()))

			case builtin.Abs:
				vm.push(runtime.Abs(vm.pop()))

			case builtin.Int:
				vm.push(runtime.ToInt(vm.pop()))

			case builtin.Float:
				vm.push(runtime.ToFloat64(vm.pop()))

			default:
				panic(fmt.Sprintf("unknown builtin %v", arg))
			}

		default:
			panic(fmt.Sprintf("unknown bytecode %#x", op))
		}

		if vm.debug {
			vm.curr <- vm.ip
		}
	}

	if vm.debug {
		close(vm.curr)
		close(vm.step)
	}

	if len(vm.stack) > 0 {
		return vm.pop(), nil
	}

	return nil, nil
}

func (vm *VM) push(value interface{}) {
	vm.stack = append(vm.stack, value)
}

func (vm *VM) current() interface{} {
	return vm.stack[len(vm.stack)-1]
}

func (vm *VM) pop() interface{} {
	value := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	return value
}

// meterInstruction charges one executed instruction and enforces the quota.
// Instruction counting happens here rather than in the main loop so the
// unlimited path pays nothing.
func (vm *VM) meterInstruction() {
	vm.work += 1
	if vm.workBudget > 0 && vm.work > vm.workBudget {
		vm.exceedBudget()
	}
	if vm.ctx != nil {
		vm.ticks++
		if vm.ticks&contextCheckMask == 0 {
			vm.checkContext()
		}
	}
}

// checkContext stops the run if the job's cancellation signal fired.
// It is called at loop iterations, around range construction and before host
// calls, and periodically from the main loop for straight-line code.
func (vm *VM) checkContext() {
	if vm.ctx == nil {
		return
	}
	if err := vm.ctx.Err(); err != nil {
		panic(err)
	}
}

// context returns the context of the current run. When the job supplied no
// context, the value found in the environment (if any) is used, which keeps
// plain Run working with an explicit context variable. A nil context is never
// handed to a host function.
func (vm *VM) context(env interface{}) context.Context {
	if vm.ctx != nil {
		return vm.ctx
	}
	if name := vm.programContextName; name != "" {
		if m, ok := env.(map[string]interface{}); ok {
			if c, ok := m[name].(context.Context); ok && c != nil {
				return c
			}
		}
	}
	return context.Background()
}

// chargeWork records work performed by the run and stops it once the job's
// quota is exhausted. Called when the run is metered.
func (vm *VM) chargeWork(units int64) {
	if units <= 0 {
		return
	}
	vm.work += units
	if vm.workBudget > 0 && vm.work > vm.workBudget {
		vm.exceedBudget()
	}
}

// exceedBudget stops the run, bound to the location of the instruction which
// pushed the work over the quota.
func (vm *VM) exceedBudget() {
	panic(&WorkBudgetExceededError{Budget: vm.workBudget, Work: vm.work})
}

// materializedSize returns how many elements a pushed constant materializes.
// Only collections are charged: values read from the environment are aliases,
// not work performed by this run. The scalar fast path keeps OpPush cheap for
// the literals which dominate real programs; other collection-like values are
// measured reflectively.
func materializedSize(value interface{}) int64 {
	switch v := value.(type) {
	case nil, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, string, complex64, complex128:
		return 0
	case []int:
		return int64(len(v))
	case []interface{}:
		return int64(len(v))
	case map[int]struct{}:
		return int64(len(v))
	case map[string]struct{}:
		return int64(len(v))
	}
	t := reflect.TypeOf(value)
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return int64(reflect.ValueOf(value).Len())
	}
	return 0
}

// WorkUsed returns how many units of work the last metered run performed.
// A plain Run (no limits) does not count work, so WorkUsed reports 0 after
// it; metering is opt-in to keep the unlimited path allocation-free.
func (vm *VM) WorkUsed() int64 {
	return vm.work
}

func (vm *VM) Stack() []interface{} {
	return vm.stack
}

func (vm *VM) Scope() *Scope {
	if len(vm.scopes) > 0 {
		return vm.scopes[len(vm.scopes)-1]
	}
	return nil
}

func (vm *VM) Step() {
	vm.step <- struct{}{}
}

func (vm *VM) Position() chan int {
	return vm.curr
}
