package expr_test

import (
	"testing"

	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/vm"
	"github.com/stretchr/testify/require"
)

func TestWitness_all_counterexample(t *testing.T) {
	program, err := expr.Compile(`all([1, 2, 3, 4], {# < 3})`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)
	require.Len(t, witnesses, 1)

	w := witnesses[0]
	require.Equal(t, "all", w.Name)
	require.True(t, w.HasElement)
	// Elements 1 and 2 satisfy the predicate, element 3 (index 2) does not.
	require.Equal(t, 2, w.Index)
	require.Nil(t, w.Inner)
	require.Equal(t, 1, w.Location.Line)
	require.Equal(t, 0, w.Location.Column)
}

func TestWitness_all_satisfied_has_no_element(t *testing.T) {
	program, err := expr.Compile(`all(1..3, {# > 0})`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Len(t, witnesses, 1)

	w := witnesses[0]
	require.Equal(t, "all", w.Name)
	require.False(t, w.HasElement)
	require.Equal(t, -1, w.Index)
}

func TestWitness_any_first_hit(t *testing.T) {
	program, err := expr.Compile(`any([1, 1, 0, 1], {# == 0})`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Len(t, witnesses, 1)

	w := witnesses[0]
	require.Equal(t, "any", w.Name)
	require.True(t, w.HasElement)
	require.Equal(t, 2, w.Index)
}

func TestWitness_any_without_hit_has_no_element(t *testing.T) {
	program, err := expr.Compile(`any([1, 2], {# == 9})`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)
	require.Len(t, witnesses, 1)

	w := witnesses[0]
	require.Equal(t, "any", w.Name)
	require.False(t, w.HasElement)
	require.Equal(t, -1, w.Index)
}

func TestWitness_empty_collections(t *testing.T) {
	allProgram, err := expr.Compile(`all([], {# > 0})`, expr.Witness())
	require.NoError(t, err)
	out, witnesses, err := expr.RunWithWitness(allProgram, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Len(t, witnesses, 1)
	require.False(t, witnesses[0].HasElement)
	require.Equal(t, -1, witnesses[0].Index)

	anyProgram, err := expr.Compile(`any([], {# > 0})`, expr.Witness())
	require.NoError(t, err)
	out, witnesses, err = expr.RunWithWitness(anyProgram, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)
	require.Len(t, witnesses, 1)
	require.False(t, witnesses[0].HasElement)
	require.Equal(t, -1, witnesses[0].Index)
}

func TestWitness_nested_chain(t *testing.T) {
	// The outer all fails on its first element; the reason is that the
	// inner any finds no matching element.
	program, err := expr.Compile(`all(1..3, { any([#], {# > 2}) })`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)
	require.Len(t, witnesses, 1)

	outer := witnesses[0]
	require.Equal(t, "all", outer.Name)
	require.True(t, outer.HasElement)
	require.Equal(t, 0, outer.Index)
	require.NotNil(t, outer.Inner)

	inner := outer.Inner
	require.Equal(t, "any", inner.Name)
	require.False(t, inner.HasElement)
	require.Equal(t, -1, inner.Index)
}

func TestWitness_nested_chain_inner_hit(t *testing.T) {
	program, err := expr.Compile(`all(1..3, { any([10, #], {# < 5}) })`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Len(t, witnesses, 1)

	// The outer all is fully satisfied, so it has no deciding element;
	// the nested chain is only meaningful together with one.
	require.False(t, witnesses[0].HasElement)
	require.Nil(t, witnesses[0].Inner)
}

func TestWitness_multiple_builtins_in_execution_order(t *testing.T) {
	program, err := expr.Compile(
		`all([1, 2], {# > 0}) && any([1, 2], {# == 9}) && all([0], {# > 0})`,
		expr.Witness(),
	)
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)

	// The third all is never reached because the any short-circuits &&.
	require.Len(t, witnesses, 2)
	require.Equal(t, "all", witnesses[0].Name)
	require.Equal(t, "any", witnesses[1].Name)
	require.False(t, witnesses[1].HasElement)
}

func TestWitness_does_not_recompute_predicates(t *testing.T) {
	// A side-effecting function counts how often the predicate really runs.
	// The witness-enabled run must invoke it exactly as many times as the
	// ordinary run; nothing is re-evaluated to build the explanation.
	mkEnv := func() (map[string]interface{}, *int) {
		calls := 0
		env := map[string]interface{}{
			"calls": func(v int) bool {
				calls++
				return true
			},
		}
		return env, &calls
	}

	plainProgram, err := expr.Compile(`all(1..5, { calls(#) })`)
	require.NoError(t, err)
	witnessProgram, err := expr.Compile(`all(1..5, { calls(#) })`, expr.Witness())
	require.NoError(t, err)

	envPlain, plainCalls := mkEnv()
	_, err = expr.Run(plainProgram, envPlain)
	require.NoError(t, err)

	envWitness, witnessCalls := mkEnv()
	_, witnesses, err := expr.RunWithWitness(witnessProgram, envWitness)
	require.NoError(t, err)
	require.Len(t, witnesses, 1)

	require.Equal(t, *plainCalls, *witnessCalls)
	require.Equal(t, 5, *witnessCalls)
}

func TestWitness_disabled_keeps_original_bytecode(t *testing.T) {
	program, err := expr.Compile(`all(1..3, {# > 0})`)
	require.NoError(t, err)

	for _, op := range []vm.Opcode{
		vm.OpWitnessPush,
		vm.OpWitnessPop,
		vm.OpWitnessJumpIfFalse,
		vm.OpWitnessJumpIfTrue,
	} {
		for _, b := range program.Bytecode {
			require.NotEqual(t, op, b)
		}
	}

	// RunWithWitness on a program compiled without the option simply
	// returns no witnesses.
	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Empty(t, witnesses)
}

func TestWitness_enabled_bytecode_contains_witness_opcodes(t *testing.T) {
	program, err := expr.Compile(`any(1..3, {# > 0})`, expr.Witness())
	require.NoError(t, err)

	var found bool
	for _, b := range program.Bytecode {
		if b == vm.OpWitnessPush {
			found = true
		}
	}
	require.True(t, found)
}

func TestWitness_disassemble(t *testing.T) {
	program, err := expr.Compile(`all(1..3, {# > 0})`, expr.Witness())
	require.NoError(t, err)
	require.NotContains(t, program.Disassemble(), "(unknown)")
}

func TestWitness_string_collection(t *testing.T) {
	program, err := expr.Compile(`all(["a", "bb", "ccc"], {len(#) > 1})`, expr.Witness())
	require.NoError(t, err)
	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.NoError(t, err)
	require.Equal(t, false, out)
	require.True(t, witnesses[0].HasElement)
	require.Equal(t, 0, witnesses[0].Index) // "a" has length 1
}

func TestWitness_predicate_panic_is_recoverable(t *testing.T) {
	program, err := expr.Compile(`all(1..3, {1 % (# - 1) == 1})`, expr.Witness())
	require.NoError(t, err)

	out, witnesses, err := expr.RunWithWitness(program, nil)
	require.Error(t, err)
	require.Nil(t, out)
	require.Empty(t, witnesses)

	// A subsequent run on the same VM must not see the failed run's state.
	runner := &vm.VM{}
	_, err = runner.Run(program, nil)
	require.Error(t, err)

	goodProgram, err := expr.Compile(`any([1, 2], {# == 2})`, expr.Witness())
	require.NoError(t, err)
	out, err = runner.Run(goodProgram, nil)
	require.NoError(t, err)
	require.Equal(t, true, out)
	require.Len(t, runner.Witnesses(), 1)
	require.Equal(t, 1, runner.Witnesses()[0].Index)
}
