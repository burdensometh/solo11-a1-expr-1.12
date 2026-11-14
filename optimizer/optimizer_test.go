package optimizer_test

import (
	"strings"
	"testing"

	"github.com/antonmedv/expr/ast"
	"github.com/antonmedv/expr/checker"
	"github.com/antonmedv/expr/conf"
	"github.com/antonmedv/expr/optimizer"
	"github.com/antonmedv/expr/parser"
	"github.com/antonmedv/expr/vm/runtime"
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

	expected := &ast.BinaryNode{
		Operator: "in",
		Left: &ast.IdentifierNode{
			Value: "age",
		},
		Right: &ast.ConstantNode{
			Value: runtime.Range{Min: 18, Max: 31},
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

func TestOptimize_const_range_empty(t *testing.T) {
	tree, err := parser.Parse(`1..-1`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.ConstantNode{
		Value: []int{},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_const_range_overflow(t *testing.T) {
	// The size of the range overflows int64. The range should not be
	// patched with a constant (in particular not with an empty slice),
	// so the memory budget detection happens on runtime.
	tree, err := parser.Parse(`(-9223372036854775807 - 1)..9223372036854775807`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.BinaryNode{
		Operator: "..",
		Left:     &ast.IntegerNode{Value: -9223372036854775807 - 1},
		Right:    &ast.IntegerNode{Value: 9223372036854775807},
	}

	assert.Equal(t, ast.Dump(expected), ast.Dump(tree.Node))
}

func TestOptimize_in_range_reversed(t *testing.T) {
	// An empty reversed range is preserved as is: the membership check
	// of any value in an empty range is false.
	tree, err := parser.Parse(`age in 31..18`)
	require.NoError(t, err)

	err = optimizer.Optimize(&tree.Node, nil)
	require.NoError(t, err)

	expected := &ast.BinaryNode{
		Operator: "in",
		Left: &ast.IdentifierNode{
			Value: "age",
		},
		Right: &ast.ConstantNode{
			Value: runtime.Range{Min: 31, Max: 18},
		},
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
