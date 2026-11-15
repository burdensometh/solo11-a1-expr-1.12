package vm_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/checker"
	"github.com/antonmedv/expr/compiler"
	"github.com/antonmedv/expr/conf"
	"github.com/antonmedv/expr/file"
	"github.com/antonmedv/expr/parser"
	"github.com/antonmedv/expr/vm"
	"github.com/stretchr/testify/require"
)

func TestRun_NilProgram(t *testing.T) {
	_, err := vm.Run(nil, nil)
	require.Error(t, err)
}

func TestRun_Debugger(t *testing.T) {
	input := `[1, 2]`

	node, err := parser.Parse(input)
	require.NoError(t, err)

	program, err := compiler.Compile(node, nil)
	require.NoError(t, err)

	debug := vm.Debug()
	go func() {
		debug.Step()
		debug.Step()
		debug.Step()
		debug.Step()
	}()
	go func() {
		for range debug.Position() {
		}
	}()

	_, err = debug.Run(program, nil)
	require.NoError(t, err)
	require.Len(t, debug.Stack(), 0)
	require.Nil(t, debug.Scope())
}

func TestRun_ReuseVM(t *testing.T) {
	node, err := parser.Parse(`map(1..2, {#})`)
	require.NoError(t, err)

	program, err := compiler.Compile(node, nil)
	require.NoError(t, err)

	reuse := vm.VM{}
	_, err = reuse.Run(program, nil)
	require.NoError(t, err)
	_, err = reuse.Run(program, nil)
	require.NoError(t, err)
}

func TestRun_Cast(t *testing.T) {
	input := `1`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	program, err := compiler.Compile(tree, &conf.Config{Expect: reflect.Float64})
	require.NoError(t, err)

	out, err := vm.Run(program, nil)
	require.NoError(t, err)

	require.Equal(t, float64(1), out)
}

func TestRun_Helpers(t *testing.T) {
	values := []interface{}{
		uint(1),
		uint8(1),
		uint16(1),
		uint32(1),
		uint64(1),
		1,
		int8(1),
		int16(1),
		int32(1),
		int64(1),
		float32(1),
		float64(1),
	}
	ops := []string{"+", "-", "*", "/", "%", "==", ">=", "<=", "<", ">"}

	for _, a := range values {
		for _, b := range values {
			for _, op := range ops {

				if op == "%" {
					switch a.(type) {
					case float32, float64:
						continue
					}
					switch b.(type) {
					case float32, float64:
						continue
					}
				}

				input := fmt.Sprintf("a %v b", op)
				env := map[string]interface{}{
					"a": a,
					"b": b,
				}

				config := conf.CreateNew()

				tree, err := parser.Parse(input)
				require.NoError(t, err)

				_, err = checker.Check(tree, config)
				require.NoError(t, err)

				program, err := compiler.Compile(tree, config)
				require.NoError(t, err)

				_, err = vm.Run(program, env)
				require.NoError(t, err)
			}
		}
	}
}

func TestRun_Helpers_Time(t *testing.T) {
	testTime := time.Date(2000, time.Month(1), 1, 0, 0, 0, 0, time.UTC)
	testDuration := time.Duration(1)

	tests := []struct {
		a       interface{}
		b       interface{}
		op      string
		want    interface{}
		wantErr bool
	}{
		{a: testTime, b: testTime, op: "<", wantErr: false, want: false},
		{a: testTime, b: testTime, op: ">", wantErr: false, want: false},
		{a: testTime, b: testTime, op: "<=", wantErr: false, want: true},
		{a: testTime, b: testTime, op: ">=", wantErr: false, want: true},
		{a: testTime, b: testTime, op: "==", wantErr: false, want: true},
		{a: testTime, b: testTime, op: "!=", wantErr: false, want: false},
		{a: testTime, b: testTime, op: "-", wantErr: false},
		{a: testTime, b: testDuration, op: "+", wantErr: false},

		// error cases
		{a: testTime, b: int64(1), op: "<", wantErr: true},
		{a: testTime, b: float64(1), op: "<", wantErr: true},
		{a: testTime, b: testDuration, op: "<", wantErr: true},

		{a: testTime, b: int64(1), op: ">", wantErr: true},
		{a: testTime, b: float64(1), op: ">", wantErr: true},
		{a: testTime, b: testDuration, op: ">", wantErr: true},

		{a: testTime, b: int64(1), op: "<=", wantErr: true},
		{a: testTime, b: float64(1), op: "<=", wantErr: true},
		{a: testTime, b: testDuration, op: "<=", wantErr: true},

		{a: testTime, b: int64(1), op: ">=", wantErr: true},
		{a: testTime, b: float64(1), op: ">=", wantErr: true},
		{a: testTime, b: testDuration, op: ">=", wantErr: true},

		{a: testTime, b: int64(1), op: "==", wantErr: false, want: false},
		{a: testTime, b: float64(1), op: "==", wantErr: false, want: false},
		{a: testTime, b: testDuration, op: "==", wantErr: false, want: false},

		{a: testTime, b: int64(1), op: "!=", wantErr: false, want: true},
		{a: testTime, b: float64(1), op: "!=", wantErr: false, want: true},
		{a: testTime, b: testDuration, op: "!=", wantErr: false, want: true},

		{a: testTime, b: int64(1), op: "-", wantErr: true},
		{a: testTime, b: float64(1), op: "-", wantErr: true},
		{a: testTime, b: testDuration, op: "-", wantErr: true},

		{a: testTime, b: testTime, op: "+", wantErr: true},
		{a: testTime, b: int64(1), op: "+", wantErr: true},
		{a: testTime, b: float64(1), op: "+", wantErr: true},
		{a: testDuration, b: testTime, op: "+", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("time helper test `%T %s %T`", tt.a, tt.op, tt.b), func(t *testing.T) {
			input := fmt.Sprintf("a %v b", tt.op)
			env := map[string]interface{}{
				"a": tt.a,
				"b": tt.b,
			}

			config := conf.CreateNew()

			tree, err := parser.Parse(input)
			require.NoError(t, err)

			_, err = checker.Check(tree, config)
			require.NoError(t, err)

			program, err := compiler.Compile(tree, config)
			require.NoError(t, err)

			got, err := vm.Run(program, env)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				if tt.want != nil {
					require.Equal(t, tt.want, got)
				}
			}
		})
	}
}

func TestRun_MemoryBudget(t *testing.T) {
	input := `map(1..100, {map(1..100, {map(1..100, {0})})})`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	_, err = vm.Run(program, nil)
	require.Error(t, err)
}

type ErrorEnv struct {
	InnerEnv InnerEnv
}
type InnerEnv struct{}

func (ErrorEnv) WillError(param string) (bool, error) {
	if param == "yes" {
		return false, errors.New("error")
	}
	return true, nil
}

func (ErrorEnv) FastError(...interface{}) interface{} {
	return true
}

func (InnerEnv) WillError(param string) (bool, error) {
	if param == "yes" {
		return false, errors.New("inner error")
	}
	return true, nil
}

func TestRun_MethodWithError(t *testing.T) {
	input := `WillError("yes")`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := ErrorEnv{}
	funcConf := conf.New(env)
	_, err = checker.Check(tree, funcConf)
	require.NoError(t, err)

	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.EqualError(t, err, "error (1:1)\n | WillError(\"yes\")\n | ^")
	require.Equal(t, nil, out)

	selfErr := errors.Unwrap(err)
	require.NotNil(t, err)
	require.Equal(t, "error", selfErr.Error())
}

func TestRun_FastMethods(t *testing.T) {
	input := `hello() + world()`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := map[string]interface{}{
		"hello": func(...interface{}) interface{} { return "hello " },
		"world": func(...interface{}) interface{} { return "world" },
	}
	funcConf := conf.New(env)
	_, err = checker.Check(tree, funcConf)
	require.NoError(t, err)

	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.NoError(t, err)

	require.Equal(t, "hello world", out)
}

func TestRun_FastMethodWithError(t *testing.T) {
	input := `FastError()`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := ErrorEnv{}
	funcConf := conf.New(env)
	_, err = checker.Check(tree, funcConf)
	require.NoError(t, err)
	require.True(t, tree.Node.(*ast.CallNode).Fast, "method must be fast")

	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.NoError(t, err)

	require.Equal(t, true, out)
}

func TestRun_InnerMethodWithError(t *testing.T) {
	input := `InnerEnv.WillError("yes")`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := ErrorEnv{}
	funcConf := conf.New(env)
	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.EqualError(t, err, "inner error (1:10)\n | InnerEnv.WillError(\"yes\")\n | .........^")
	require.Equal(t, nil, out)
}

func TestRun_InnerMethodWithError_NilSafe(t *testing.T) {
	input := `InnerEnv?.WillError("yes")`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := ErrorEnv{}
	funcConf := conf.New(env)
	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.EqualError(t, err, "inner error (1:11)\n | InnerEnv?.WillError(\"yes\")\n | ..........^")
	require.Equal(t, nil, out)
}

func TestRun_TaggedFieldName(t *testing.T) {
	input := `value`

	tree, err := parser.Parse(input)
	require.NoError(t, err)

	env := struct {
		V string `expr:"value"`
	}{
		V: "hello world",
	}

	funcConf := conf.New(env)
	_, err = checker.Check(tree, funcConf)
	require.NoError(t, err)

	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	out, err := vm.Run(program, env)
	require.NoError(t, err)

	require.Equal(t, "hello world", out)
}

func TestRun_OpInvalid(t *testing.T) {
	program := &vm.Program{
		Locations: []file.Location{{0, 0}},
		Bytecode:  []vm.Opcode{vm.OpInvalid},
		Arguments: []int{0},
	}

	_, err := vm.Run(program, nil)
	require.EqualError(t, err, "invalid opcode")
}

func TestRun_WorkBudget(t *testing.T) {
	tree, err := parser.Parse(`map(1..100, {# * 2})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	// Measure the exact cost with a generous quota (metering is opt-in).
	v := vm.VM{}
	_, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: 1 << 40})
	require.NoError(t, err)
	used := v.WorkUsed()
	require.Greater(t, used, int64(0))

	// The same work, with a quota below it, stops this evaluation only.
	_, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: used - 1})
	require.Error(t, err)
	assertWorkBudgetError(t, err)

	// The compiled program is untouched: the next job still evaluates it.
	out, err := v.RunWithLimits(program, nil, vm.Limits{WorkBudget: used})
	require.NoError(t, err)
	require.Equal(t, []interface{}{2, 4}, take(out, 2))

	// A quota exactly at the cost of the run is enough.
	out, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: v.WorkUsed()})
	require.NoError(t, err)
	require.NotNil(t, out)
}

func TestRun_WorkBudgetLocation(t *testing.T) {
	tree, err := parser.Parse(`len(map(1..100, {#}))`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	_, err = vm.RunWithLimits(program, nil, vm.Limits{WorkBudget: 5})
	require.Error(t, err)
	assertWorkBudgetError(t, err)
	// The error points at the loop that exhausted the quota rather than at
	// the start of the expression, and still carries the source line.
	require.Contains(t, err.Error(), "(1:")
	require.Contains(t, err.Error(), "len(map(1..100, {#}))")
	require.Contains(t, err.Error(), "|")
}

func TestRun_WorkBudgetStopsRunNotProgram(t *testing.T) {
	tree, err := parser.Parse(`1..100000`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	// This range is not folded at compile time (its size exceeds the const
	// range limit), so its construction must be metered on its own.
	_, err = vm.RunWithLimits(program, nil, vm.Limits{WorkBudget: 10})
	require.Error(t, err)
	assertWorkBudgetError(t, err)

	for i := 0; i < 3; i++ {
		out, err := vm.RunWithLimits(program, nil, vm.Limits{})
		require.NoError(t, err)
		require.Len(t, out, 100000)
	}
}

func TestRun_WorkBudgetOptimizedPaths(t *testing.T) {
	// Explicit work quotas must not be bypassed by the optimizer: a range
	// folded into a constant, or an "in" rewritten to a map lookup, still
	// costs at least as much as materializing the collection.
	tests := []struct {
		code    string
		minWork int64
	}{
		{code: `1..100`, minWork: 100},                              // const range
		{code: `map(1..100, {#})`, minWork: 100},                    // loop
		{code: `2 in [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]`, minWork: 10}, // in array
		{code: `2 in 1..10`, minWork: 0},                            // in range
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			tree, err := parser.Parse(tt.code)
			require.NoError(t, err)
			program, err := compiler.Compile(tree, nil)
			require.NoError(t, err)

			v := vm.VM{}
			_, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: 1 << 40})
			require.NoError(t, err)
			require.GreaterOrEqual(t, v.WorkUsed(), tt.minWork)

			// The same program with an insufficient quota fails, without
			// affecting a later run.
			_, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: 1})
			require.Error(t, err)
			assertWorkBudgetError(t, err)

			_, err = v.RunWithLimits(program, nil, vm.Limits{})
			require.NoError(t, err)
		})
	}
}

func TestRun_WorkBudgetDoesNotAffectPlainRun(t *testing.T) {
	tree, err := parser.Parse(`1..100000`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	out, err := vm.Run(program, nil)
	require.NoError(t, err)
	require.Len(t, out, 100000)
}

func TestRun_ContextCancellation(t *testing.T) {
	tree, err := parser.Parse(`all(1..100000, {# > 0})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, err.Error(), "all(1..100000, {# > 0})")

	// Cancellation of one job does not affect the program.
	out, err := vm.Run(program, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
}

func TestRun_ContextCancellationNested(t *testing.T) {
	// Each loop builtin must observe the cancellation of the job, including
	// when nested inside another predicate.
	tests := []string{
		`map(1..300, {any(1..300, {all(1..300, {# > 0})})})`,
		`filter(1..100000, {# % 2 == 0})`,
		`count(1..100000, {# > 0})`,
		`none(1..100000, {# < 0})`,
	}
	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			tree, err := parser.Parse(code)
			require.NoError(t, err)
			program, err := compiler.Compile(tree, nil)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
			require.Error(t, err)
			require.ErrorIs(t, err, context.Canceled)
			require.Contains(t, err.Error(), code)
		})
	}
}

func TestRun_ContextCancellationDeadline(t *testing.T) {
	// A range of 300000 elements stays under the memory budget, so the run
	// gets far enough for the deadline to be the reason it stops.
	tree, err := parser.Parse(`count(1..300000, {# > 0})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "(1:1)")
}

func TestRun_ContextCancellationAtRange(t *testing.T) {
	tree, err := parser.Parse(`1..1000000`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRun_ContextPassedToFunction(t *testing.T) {
	env := map[string]interface{}{
		"ctx": context.TODO(),
		"fn":  func(ctx context.Context) int { return ctx.Value("value").(int) },
	}
	tree, err := parser.Parse(`fn()`)
	require.NoError(t, err)
	funcConf := conf.New(env)
	funcConf.ContextName = "ctx"
	patcher := &conf.ContextPatcher{Config: funcConf}
	// Patchers run between checker passes in expr.Compile because some of
	// them rely on the types inferred by the checker.
	// The first pass reports the missing context argument; the compiler
	// ignores errors of visitor passes for this reason.
	_, _ = checker.Check(tree, funcConf)
	ast.Walk(&tree.Node, patcher)
	_, err = checker.Check(tree, funcConf)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, funcConf)
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), "value", 42)
	out, err := vm.RunWithLimits(program, env, vm.Limits{Context: ctx})
	require.NoError(t, err)
	require.Equal(t, 42, out)

	// And the same program works with plain Run using the env's value.
	env["ctx"] = context.WithValue(context.Background(), "value", 1)
	out, err = vm.Run(program, env)
	require.NoError(t, err)
	require.Equal(t, 1, out)
}

func take(value interface{}, n int) []interface{} {
	out := []interface{}{}
	array := value.([]interface{})
	if n > len(array) {
		n = len(array)
	}
	for i := 0; i < n; i++ {
		out = append(out, array[i])
	}
	return out
}

func assertWorkBudgetError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, err, vm.ErrWorkBudgetExceeded)
	var exceeded *vm.WorkBudgetExceededError
	require.ErrorAs(t, err, &exceeded)
	require.Contains(t, err.Error(), "work budget")
}

func TestRun_WorkBudgetIsPerJobWithSharedVM(t *testing.T) {
	tree, err := parser.Parse(`map(1..50, {#})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	// One VM object is reused for jobs with completely different quotas;
	// neither the quota nor the consumed work of one job leaks into the next.
	v := vm.VM{}
	_, err = v.RunWithLimits(program, nil, vm.Limits{WorkBudget: 20})
	require.Error(t, err)
	assertWorkBudgetError(t, err)

	out, err := v.RunWithLimits(program, nil, vm.Limits{WorkBudget: 1000})
	require.NoError(t, err)
	require.Len(t, out, 50)

	// And an unlimited job after a failed one is not affected either.
	out, err = v.Run(program, nil)
	require.NoError(t, err)
	require.Len(t, out, 50)
}

func TestRun_WorkMeteredByActualWorkNotResultLength(t *testing.T) {
	// The predicates select nothing, so the result is empty/shorter than the
	// collection; the work still grows with the iteration and must be counted.
	tests := []string{
		`filter(1..100000, {false})`,
		`count(1..100000, {false})`,
		`map(1..100000, {0})`,
		`all(1..100000, {false})`,
		`none(1..100000, {false})`,
	}
	for _, code := range tests {
		t.Run(code, func(t *testing.T) {
			tree, err := parser.Parse(code)
			require.NoError(t, err)
			program, err := compiler.Compile(tree, nil)
			require.NoError(t, err)

			_, err = vm.RunWithLimits(program, nil, vm.Limits{WorkBudget: 1000})
			require.Error(t, err)
			assertWorkBudgetError(t, err)
		})
	}
}

func TestRun_ContextCancellationMidLoop(t *testing.T) {
	// Cancellation arrives while the loop is already running; the loop
	// observes it on the next iteration rather than at the start of the run.
	tree, err := parser.Parse(`map(1..100000, {#})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
	<-done
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	// The program is intact afterwards.
	out, err := vm.Run(program, nil)
	require.NoError(t, err)
	require.Len(t, out, 100000)
}

func TestRun_CancelledJobReportsCancellationFirst(t *testing.T) {
	// A job cancelled before it starts reports the cancellation even when it
	// also carries a tiny work quota, so callers can tell the two apart.
	tree, err := parser.Parse(`map(1..100, {#})`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx, WorkBudget: 1})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, vm.ErrWorkBudgetExceeded)
}

func TestRun_ContextAtMaterializedConstant(t *testing.T) {
	// A range folded at compile time is pushed as one constant; materializing
	// it must still observe cancellation like runtime range construction.
	tree, err := parser.Parse(`1..500000`)
	require.NoError(t, err)
	program, err := compiler.Compile(tree, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = vm.RunWithLimits(program, nil, vm.Limits{Context: ctx})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}
