package feature_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/feature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Features may be defined in any order; the dependency graph, not the
// configuration order, determines the evaluation.
func TestSet_DefinitionOrderDoesNotMatter(t *testing.T) {
	defs := [][]feature.Feature{
		{
			{Name: "a", Expr: `x + 1`},
			{Name: "b", Expr: `a + 1`},
			{Name: "c", Expr: `b + 1`},
		},
		{
			{Name: "c", Expr: `b + 1`},
			{Name: "b", Expr: `a + 1`},
			{Name: "a", Expr: `x + 1`},
		},
		{
			{Name: "b", Expr: `a + 1`},
			{Name: "c", Expr: `b + 1`},
			{Name: "a", Expr: `x + 1`},
		},
	}

	for _, def := range defs {
		set, err := feature.Compile(def, expr.Env(map[string]interface{}{"x": 0}))
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, set.EvaluationOrder())

		results := set.Run(map[string]interface{}{"x": 1})
		require.True(t, results.OK(), "%v", results)
		assert.Equal(t, 2, results["a"].Value)
		assert.Equal(t, 3, results["b"].Value)
		assert.Equal(t, 4, results["c"].Value)
	}
}

// At compile time the set distinguishes input fields, feature references,
// and closure locals.
func TestSet_CompileTimeClassification(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "threshold", Expr: `limit * 2`},
		{Name: "total", Expr: `threshold + tax`},
		{Name: "big", Expr: `filter(list, {# > threshold})`},
		{Name: "scaled", Expr: `map(list, {# * factor})`},
	}, expr.Env(map[string]interface{}{
		"limit":  0,
		"tax":    0,
		"list":   []int{},
		"factor": 0,
	}))
	require.NoError(t, err)

	// Input fields.
	inputs, ok := set.Inputs("threshold")
	require.True(t, ok)
	assert.Equal(t, []string{"limit"}, inputs)

	// Feature references.
	deps, ok := set.Dependencies("total")
	require.True(t, ok)
	assert.Equal(t, []string{"threshold"}, deps)
	inputs, _ = set.Inputs("total")
	assert.Equal(t, []string{"tax"}, inputs)

	// The closure pointer # is a closure local, never a reference; names
	// from the enclosing scope used inside a closure still classify.
	deps, _ = set.Dependencies("big")
	assert.Equal(t, []string{"threshold"}, deps)
	inputs, _ = set.Inputs("big")
	assert.Equal(t, []string{"list"}, inputs)

	deps, _ = set.Dependencies("scaled")
	assert.Empty(t, deps)
	inputs, _ = set.Inputs("scaled")
	assert.Equal(t, []string{"factor", "list"}, inputs)

	results := set.Run(map[string]interface{}{
		"limit":  3,
		"tax":    1,
		"list":   []int{1, 5, 8},
		"factor": 10,
	})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 6, results["threshold"].Value)
	assert.Equal(t, 7, results["total"].Value)
	assert.Equal(t, []interface{}{8}, results["big"].Value)
	assert.Equal(t, []interface{}{10, 50, 80}, results["scaled"].Value)
}

// Cycles are rejected at compile time with the full dependency path.
func TestSet_CycleReportsFullPath(t *testing.T) {
	_, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `c + 1`},
		{Name: "b", Expr: `a + 1`},
		{Name: "c", Expr: `b + 1`},
		{Name: "independent", Expr: `1`},
	})
	require.Error(t, err)

	var cycle *feature.CycleError
	require.True(t, errors.As(err, &cycle), "got %T: %v", err, err)
	assert.Equal(t, []string{"a", "c", "b", "a"}, cycle.Path)
	assert.Equal(t, "feature: circular dependency detected: a -> c -> b -> a", err.Error())
}

func TestSet_SelfDependencyCycle(t *testing.T) {
	_, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `a + 1`},
	})
	require.Error(t, err)

	var cycle *feature.CycleError
	require.True(t, errors.As(err, &cycle))
	assert.Equal(t, []string{"a", "a"}, cycle.Path)
	assert.Equal(t, "feature: circular dependency detected: a -> a", err.Error())
}

// Every feature is computed at most once per run, even when it is a shared
// dependency of several branches.
func TestSet_SharedIntermediateValues(t *testing.T) {
	calls := 0
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `touch(1)`},
		{Name: "b", Expr: `a + 1`},
		{Name: "c", Expr: `a + 2`},
		{Name: "d", Expr: `b + c`},
	}, expr.Function("touch", func(params ...interface{}) (interface{}, error) {
		calls++
		return params[0], nil
	}))
	require.NoError(t, err)

	results := set.Run(nil)
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 1, results["a"].Value)
	assert.Equal(t, 2, results["b"].Value)
	assert.Equal(t, 3, results["c"].Value)
	assert.Equal(t, 5, results["d"].Value)
	assert.Equal(t, 1, calls, "shared dependency must be computed exactly once")
}

// A failing feature blocks only its dependent branch; independent features
// still return values and statuses.
func TestSet_FailureBlocksOnlyDependents(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "boom", Expr: `10 % x`}, // integer modulo by zero at run time
		{Name: "direct", Expr: `boom + 1`},
		{Name: "transitive", Expr: `direct + 1`},
		{Name: "free", Expr: `40 + 2`},
		{Name: "alsoFree", Expr: `free * 2`},
	}, expr.Env(map[string]interface{}{"x": 0}))
	require.NoError(t, err)

	results := set.Run(map[string]interface{}{"x": 0})
	require.Len(t, results, 5)

	// The failing feature reports its own error.
	assert.Equal(t, feature.StatusError, results["boom"].Status)
	require.Error(t, results["boom"].Err)
	assert.Nil(t, results["boom"].Value)

	// Dependents are blocked, directly and transitively.
	assert.Equal(t, feature.StatusBlocked, results["direct"].Status)
	var blocked *feature.BlockedError
	require.True(t, errors.As(results["direct"].Err, &blocked))
	assert.Equal(t, "direct", blocked.Feature)
	assert.Equal(t, "boom", blocked.Dependency)

	assert.Equal(t, feature.StatusBlocked, results["transitive"].Status)
	require.True(t, errors.As(results["transitive"].Err, &blocked))
	assert.Equal(t, "transitive", blocked.Feature)
	assert.Equal(t, "direct", blocked.Dependency)

	// Independent features still return values and statuses.
	assert.Equal(t, feature.StatusOK, results["free"].Status)
	assert.Equal(t, 42, results["free"].Value)
	assert.NoError(t, results["free"].Err)

	assert.Equal(t, feature.StatusOK, results["alsoFree"].Status)
	assert.Equal(t, 84, results["alsoFree"].Value)

	value, ok := results.Value("free")
	assert.True(t, ok)
	assert.Equal(t, 42, value)
	_, ok = results.Value("boom")
	assert.False(t, ok)
	assert.False(t, results.OK())
}

// An input field whose name collides with a feature name never creates a
// dependency: references resolve to the input, at compile time and at run
// time.
func TestSet_SameNameInputCreatesNoDependency(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "rate", Expr: `base * 2`},   // a feature named like the input
		{Name: "total", Expr: `rate * 10`}, // reads the input field, not the feature
	}, expr.Env(map[string]interface{}{
		"rate": 0,
		"base": 0,
	}))
	require.NoError(t, err)

	// No dependency on the feature "rate" is created for "total".
	deps, ok := set.Dependencies("total")
	require.True(t, ok)
	assert.Empty(t, deps)
	inputs, ok := set.Inputs("total")
	require.True(t, ok)
	assert.Equal(t, []string{"rate"}, inputs)

	// The feature itself is still computed and reported, but references to
	// "rate" read the input field.
	results := set.Run(map[string]interface{}{"rate": 3, "base": 10})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 20, results["rate"].Value)  // the feature's own value
	assert.Equal(t, 30, results["total"].Value) // input rate (3) wins
}

type Inner struct {
	Factor int
}

type structEnvConfig struct {
	Inner
	Names  []string
	Prefix string `expr:"tag"`
}

func (c structEnvConfig) Shout(s string) string { return strings.ToUpper(s) + "!" }

// Struct environments are supported, including promoted fields of embedded
// structs, expr tags, and methods.
func TestSet_StructEnv(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "scaled", Expr: `Factor * 2`},
		{Name: "greeting", Expr: `Shout(tag + "-" + Names[0])`},
		{Name: "summary", Expr: `scaled + len(Names)`},
	}, expr.Env(structEnvConfig{}))
	require.NoError(t, err)

	inputs, _ := set.Inputs("greeting")
	assert.Equal(t, []string{"Names", "Shout", "tag"}, inputs)

	results := set.Run(structEnvConfig{
		Inner:  Inner{Factor: 3},
		Names:  []string{"expr"},
		Prefix: "hi",
	})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 6, results["scaled"].Value)
	assert.Equal(t, "HI-EXPR!", results["greeting"].Value)
	assert.Equal(t, 7, results["summary"].Value)
}

// A feature set can be evaluated without declaring an environment; unknown
// names are then treated as input fields read at run time.
func TestSet_NoEnvSpec(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "doubled", Expr: `n * 2`},
		{Name: "quad", Expr: `doubled * 2`},
	})
	require.NoError(t, err)

	results := set.Run(map[string]interface{}{"n": 3})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 6, results["doubled"].Value)
	assert.Equal(t, 12, results["quad"].Value)
}

// With a strict environment, references to undeclared names fail at compile
// time and name the feature.
func TestSet_UnknownNameFailsCompile(t *testing.T) {
	_, err := feature.Compile([]feature.Feature{
		{Name: "bad", Expr: `x + y`},
	}, expr.Env(map[string]interface{}{"x": 1}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `feature "bad"`)
	assert.Contains(t, err.Error(), "unknown name y")
}

// A syntax error is reported with the feature name.
func TestSet_SyntaxErrorNamesFeature(t *testing.T) {
	_, err := feature.Compile([]feature.Feature{
		{Name: "broken", Expr: `1 +`},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `feature "broken"`)
}

func TestSet_InvalidAndDuplicateNames(t *testing.T) {
	for _, name := range []string{"1bad", "with space", "env", "nil", "true", "and", "not", "in", "all", "filter", "map"} {
		_, err := feature.Compile([]feature.Feature{{Name: name, Expr: `1`}})
		require.Error(t, err, "name %q should be rejected", name)
	}

	_, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `1`},
		{Name: "a", Expr: `2`},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate feature name "a"`)

	_, err = feature.Compile([]feature.Feature{
		{Name: "touch", Expr: `1`},
	}, expr.Function("touch", func(params ...interface{}) (interface{}, error) {
		return nil, nil
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with a function")
}

func TestSet_EmptySet(t *testing.T) {
	set, err := feature.Compile(nil)
	require.NoError(t, err)
	assert.Empty(t, set.Names())
	assert.Empty(t, set.EvaluationOrder())
	assert.Empty(t, set.Run(nil))
	assert.True(t, set.Run(nil).OK())
}

func TestSet_API(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "c", Expr: `b + 1`},
		{Name: "a", Expr: `1`},
		{Name: "b", Expr: `a + 1`},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"c", "a", "b"}, set.Names())
	assert.Equal(t, []string{"a", "b", "c"}, set.EvaluationOrder())

	_, ok := set.Dependencies("missing")
	assert.False(t, ok)
	_, ok = set.Inputs("missing")
	assert.False(t, ok)

	// Returned slices are copies.
	deps, _ := set.Dependencies("c")
	deps[0] = "mutated"
	deps, _ = set.Dependencies("c")
	assert.Equal(t, []string{"b"}, deps)
}

// A run-time error returned by a function fails only that feature.
func TestSet_FunctionErrorIsIsolated(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "failing", Expr: `fail()`},
		{Name: "dependent", Expr: `failing + 1`},
		{Name: "independent", Expr: `7`},
	}, expr.Function("fail", func(params ...interface{}) (interface{}, error) {
		return nil, errors.New("nope")
	}))
	require.NoError(t, err)

	results := set.Run(nil)
	assert.Equal(t, feature.StatusError, results["failing"].Status)
	assert.Contains(t, results["failing"].Err.Error(), "nope")
	assert.Equal(t, feature.StatusBlocked, results["dependent"].Status)
	assert.Equal(t, feature.StatusOK, results["independent"].Status)
	assert.Equal(t, 7, results["independent"].Value)
}

// The same set can be evaluated concurrently with different inputs.
func TestSet_ConcurrentRuns(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `x * 2`},
		{Name: "b", Expr: `a + x`},
	}, expr.Env(map[string]interface{}{"x": 0}))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()
			results := set.Run(map[string]interface{}{"x": x})
			if !results.OK() {
				t.Errorf("run %d: %v", x, results)
				return
			}
			if results["b"].Value != x*3 {
				t.Errorf("run %d: got %v", x, results["b"].Value)
			}
		}(i)
	}
	wg.Wait()
}

// The caller's environment map is never mutated by a run.
func TestSet_EnvNotMutated(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `x + 1`},
	}, expr.Env(map[string]interface{}{"x": 0}))
	require.NoError(t, err)

	env := map[string]interface{}{"x": 1}
	results := set.Run(env)
	require.True(t, results.OK())
	assert.Equal(t, map[string]interface{}{"x": 1}, env)
}

// Runs of the same set are independent of each other.
func TestSet_RunsAreIndependent(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `x + 1`},
		{Name: "b", Expr: `a * 10`},
	}, expr.Env(map[string]interface{}{"x": 0}))
	require.NoError(t, err)

	first := set.Run(map[string]interface{}{"x": 1})
	second := set.Run(map[string]interface{}{"x": 5})

	assert.Equal(t, 2, first["a"].Value)
	assert.Equal(t, 20, first["b"].Value)
	assert.Equal(t, 6, second["a"].Value)
	assert.Equal(t, 60, second["b"].Value)
}

func TestSet_StatusString(t *testing.T) {
	assert.Equal(t, "ok", feature.StatusOK.String())
	assert.Equal(t, "error", feature.StatusError.String())
	assert.Equal(t, "blocked", feature.StatusBlocked.String())
}

// Identifier map keys are names, not references; computed keys are.
func TestSet_MapLiteralKeys(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "k", Expr: `"total"`},
		{Name: "literal", Expr: `{k: 1}`},
		{Name: "computed", Expr: `{(k): 2}`},
	})
	require.NoError(t, err)

	deps, _ := set.Dependencies("literal")
	assert.Empty(t, deps, "an identifier map key is a name, not a reference")
	deps, _ = set.Dependencies("computed")
	assert.Equal(t, []string{"k"}, deps)

	results := set.Run(nil)
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, map[string]interface{}{"k": 1}, results["literal"].Value)
	assert.Equal(t, map[string]interface{}{"total": 2}, results["computed"].Value)
}

// env.field classifies exactly like a top-level reference to field.
func TestSet_EnvKeyword(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "factor", Expr: `2`},
		{Name: "scaled", Expr: `env.limit * env.factor`},
	}, expr.Env(map[string]interface{}{"limit": 0}))
	require.NoError(t, err)

	inputs, _ := set.Inputs("scaled")
	assert.Equal(t, []string{"limit"}, inputs)
	deps, _ := set.Dependencies("scaled")
	assert.Equal(t, []string{"factor"}, deps)

	results := set.Run(map[string]interface{}{"limit": 21})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 42, results["scaled"].Value)
}

// Non-interface map environments are supported.
func TestSet_TypedMapEnv(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `x + 1`},
	}, expr.Env(map[string]int{"x": 0}))
	require.NoError(t, err)

	results := set.Run(map[string]int{"x": 41})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 42, results["a"].Value)
}

// Undeclared names are input fields when the environment allows them.
func TestSet_AllowUndefinedVariables(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `x + y`},
	}, expr.Env(map[string]interface{}{"x": 0}), expr.AllowUndefinedVariables())
	require.NoError(t, err)

	inputs, _ := set.Inputs("a")
	assert.Equal(t, []string{"x", "y"}, inputs)

	results := set.Run(map[string]interface{}{"x": 1, "y": 2})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 3, results["a"].Value)
}

type ptrEnv struct{ N int }

func (p *ptrEnv) Inc() int { return p.N + 1 }

// Pointer environments expose pointer receiver methods.
func TestSet_PointerEnv(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `Inc() * N`},
	}, expr.Env(&ptrEnv{}))
	require.NoError(t, err)

	results := set.Run(&ptrEnv{N: 2})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, 6, results["a"].Value)
}

// Map environments may provide functions as input fields.
func TestSet_MapEnvFunctions(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "greeting", Expr: `sprintf("%s #%d", name, rank)`},
	}, expr.Env(map[string]interface{}{
		"sprintf": fmt.Sprintf,
		"name":    "",
		"rank":    0,
	}))
	require.NoError(t, err)

	inputs, _ := set.Inputs("greeting")
	assert.Equal(t, []string{"name", "rank", "sprintf"}, inputs)

	results := set.Run(map[string]interface{}{
		"sprintf": fmt.Sprintf,
		"name":    "expr",
		"rank":    1,
	})
	require.True(t, results.OK(), "%v", results)
	assert.Equal(t, "expr #1", results["greeting"].Value)
}

func TestSet_BlockedErrorMessage(t *testing.T) {
	set, err := feature.Compile([]feature.Feature{
		{Name: "a", Expr: `1 % zero`},
		{Name: "b", Expr: `a + 1`},
	}, expr.Env(map[string]interface{}{"zero": 0}))
	require.NoError(t, err)

	results := set.Run(map[string]interface{}{"zero": 0})
	require.Equal(t, feature.StatusBlocked, results["b"].Status)
	assert.Equal(t, `feature "b" blocked: dependency "a" failed`, results["b"].Err.Error())
	assert.True(t, strings.Contains(results["a"].Err.Error(), "integer divide by zero"))
}

// Example of the whole flow.
func Example() {
	set, err := feature.Compile([]feature.Feature{
		{Name: "net", Expr: `price * amount`},
		{Name: "vat", Expr: `net * rate`},
		{Name: "gross", Expr: `net + vat`},
	}, expr.Env(map[string]interface{}{
		"price":  0.0,
		"amount": 0,
		"rate":   0.0,
	}))
	if err != nil {
		panic(err)
	}

	results := set.Run(map[string]interface{}{
		"price":  10.0,
		"amount": 2,
		"rate":   0.2,
	})
	for _, name := range set.EvaluationOrder() {
		fmt.Printf("%s (%s): %v\n", name, results[name].Status, results[name].Value)
	}
	// Output:
	// net (ok): 20
	// vat (ok): 4
	// gross (ok): 24
}

func ExampleSet_Run_partialFailure() {
	set, err := feature.Compile([]feature.Feature{
		{Name: "head", Expr: `1 % risk`},
		{Name: "downstream", Expr: `head * 100`},
		{Name: "independent", Expr: `40 + 2`},
	}, expr.Env(map[string]interface{}{"risk": 0}))
	if err != nil {
		panic(err)
	}

	results := set.Run(map[string]interface{}{"risk": 0})
	for _, name := range []string{"head", "downstream", "independent"} {
		r := results[name]
		fmt.Printf("%s: %s (value=%v)\n", name, r.Status, r.Value)
	}
	// Output:
	// head: error (value=<nil>)
	// downstream: blocked (value=<nil>)
	// independent: ok (value=42)
}
