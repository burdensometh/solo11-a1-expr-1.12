package optimizer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/checker"
	"github.com/antonmedv/expr/conf"
	"github.com/antonmedv/expr/optimizer"
	"github.com/antonmedv/expr/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimize_constant_folding(t *testing.T) {
	tree, err := parser.Parse(`[1,2,3][5*5-25]`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.MemberNode{
		Node:     &ast.ConstantNode{Value: []interface{}{1, 2, 3}},
		Property: &ast.IntegerNode{Value: 0},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_constant_folding_with_floats(t *testing.T) {
	tree, err := parser.Parse(`1 + 2.0 * ((1.0 * 2) / 2) - 0`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.FloatNode{Value: 3.0}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_constant_folding_with_bools(t *testing.T) {
	tree, err := parser.Parse(`(true and false) or (true or false) or (false and false) or (true and (true == false))`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.BoolNode{Value: true}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_in_array(t *testing.T) {
	config := conf.New(map[string]int{"v": 0})

	tree, err := parser.Parse(`v in [1,2,3]`)
	require.NoError(t, err)

	_, err = checker.Check(tree, config)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.BinaryNode{
		Operator: "in",
		Left:     &ast.IdentifierNode{Value: "v"},
		Right:    &ast.ConstantNode{Value: map[int]struct{}{1: {}, 2: {}, 3: {}}},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_in_range(t *testing.T) {
	tree, err := parser.Parse(`age in 18..31`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	left := &ast.IdentifierNode{
		Value: "age",
	}
	expected := &ast.BinaryNode{
		Operator: "and",
		Left: &ast.BinaryNode{
			Operator: ">=",
			Left:     left,
			Right: &ast.IntegerNode{
				Value: 18,
			},
		},
		Right: &ast.BinaryNode{
			Operator: "<=",
			Left:     left,
			Right: &ast.IntegerNode{
				Value: 31,
			},
		},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_const_range(t *testing.T) {
	tree, err := parser.Parse(`-1..1`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.ConstantNode{
		Value: []int{-1, 0, 1},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_const_expr(t *testing.T) {
	tree, err := parser.Parse(`upper("hello")`)
	require.NoError(t, err)

	env := map[string]interface{}{
		"upper": strings.ToUpper,
	}

	config := conf.New(env)
	config.ConstExpr("upper")

	err = optimizer.Optimize(&tree.Node, config)
	require.NoError(t, err)

	expected := &ast.ConstantNode{
		Value: "HELLO",
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_unreachable_branch_is_not_folded(t *testing.T) {
	tests := []string{
		`false and (1 % 0 == 0)`,
		`true or (1 % 0 == 0)`,
		`false ? 1 % 0 : 42`,
		`true ? 42 : 1 % 0`,
	}
	for _, input := range tests {
		tree, err := parser.Parse(input)
		require.NoError(t, err)

		err = optimizer.Optimize(&tree.Node, nil)
		require.NoError(t, err, input)
	}
}

func TestOptimize_uncertain_branch_error_is_left_to_runtime(t *testing.T) {
	// The condition cannot be resolved at compile time, so the division by
	// zero may or may not happen and must not fail the compilation.
	tree, err := parser.Parse(`flag ? 42 : 1 % 0`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	_, ok := tree.Node.(*ast.ConditionalNode)
	assert.True(t, ok, "conditional with uncertain condition must be kept")
}

func TestOptimize_reachable_division_by_zero_still_fails(t *testing.T) {
	tests := []string{
		`1 % 0`,
		`true and (1 % 0 == 0)`,
		`false or (1 % 0 == 0)`,
		`true ? 1 % 0 : 42`,
		`false ? 42 : 1 % 0`,
		// Operators other than "and"/"or" always evaluate both sides, so the
		// error is certain even with a non-constant left operand.
		`true == (1 % 0 == 0)`,
		`flag == (1 % 0 == 0)`,
	}
	for _, input := range tests {
		tree, err := parser.Parse(input)
		require.NoError(t, err)

		err = optimizer.Optimize(&tree.Node, nil)
		assert.Error(t, err, input)
	}
}

func TestOptimize_short_circuit_keeps_effective_branch_location(t *testing.T) {
	tree, err := parser.Parse(`false or ok`)
	require.NoError(t, err)

	_, err = checker.Check(tree, conf.New(map[string]bool{"ok": true}))
	require.NoError(t, err)

	require.NoError(t, optimizer.Optimize(&tree.Node, nil))

	ident, ok := tree.Node.(*ast.IdentifierNode)
	require.True(t, ok)
	assert.Equal(t, "ok", ident.Value)
	assert.Equal(t, ident.Location(), tree.Node.Location())
}

func TestOptimize_const_expr_not_evaluated_in_unreachable_branch(t *testing.T) {
	env := map[string]interface{}{
		"boom": func() (int, error) {
			t.Fatal("ConstExpr function must not be called in unreachable branch")
			return 0, nil
		},
		"ok": func() int {
			return 42
		},
	}
	config := conf.New(env)
	config.ConstExpr("boom")
	config.ConstExpr("ok")

	tests := []string{
		`false and boom() == 0`,
		`true or boom() == 0`,
		`false ? boom() : ok()`,
		`true ? ok() : boom()`,
	}
	for _, input := range tests {
		tree, err := parser.Parse(input)
		require.NoError(t, err)
		_, err = checker.Check(tree, config)
		require.NoError(t, err)

		require.NoError(t, optimizer.Optimize(&tree.Node, config), input)
	}
}

func TestOptimize_const_expr_uncertain_branch_is_left_to_runtime(t *testing.T) {
	called := false
	env := map[string]interface{}{
		"flag": true,
		"boom": func() (int, error) {
			called = true
			return 0, errors.New("boom")
		},
	}
	config := conf.New(env)
	config.ConstExpr("boom")

	tree, err := parser.Parse(`flag ? 1 : boom()`)
	require.NoError(t, err)
	_, err = checker.Check(tree, config)
	require.NoError(t, err)

	require.NoError(t, optimizer.Optimize(&tree.Node, config))
	assert.False(t, called, "ConstExpr function in an uncertain branch must not be called")

	// The call node is still present and will be executed by the VM.
	_, ok := tree.Node.(*ast.ConditionalNode)
	assert.True(t, ok)
}

func TestOptimize_const_expr_still_evaluated_on_certain_path(t *testing.T) {
	called := false
	env := map[string]interface{}{
		"ok": func() int {
			called = true
			return 42
		},
	}
	config := conf.New(env)
	config.ConstExpr("ok")

	tree, err := parser.Parse(`ok()`)
	require.NoError(t, err)
	_, err = checker.Check(tree, config)
	require.NoError(t, err)

	require.NoError(t, optimizer.Optimize(&tree.Node, config))
	assert.True(t, called)

	constant, ok := tree.Node.(*ast.ConstantNode)
	require.True(t, ok)
	assert.Equal(t, 42, constant.Value)
}
