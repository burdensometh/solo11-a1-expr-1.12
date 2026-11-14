# Internals

Expr is a stack-based virtual machine. It compiles expressions to bytecode and
runs it. The compilation is done in a few steps:

- Parse expression to AST
- Type check AST
    - Apply operator overloading
    - Apply custom AST patching
- Optimize AST
- Compile AST to a bytecode

The compiler has a bunch of optimization which will produce a more optimal program.

## In array

```
value in ['foo', 'bar', 'baz']
```

If Expr finds an `in` or `not in` expression with an array, it will be
transformed into:

```
value in {"foo": true, "bar": true, "baz": true}
```

## Constant folding

Arithmetic expressions with constants are computed on compile step and replaced
with the result.

```
-(2 - 5) ** 3 - 2 / (+4 - 3) + -2
```

Will be compiled into just a single number:

```
23
```

## In range

```
user.Age in 18..32
```

Will be replaced with a binary operator:

```
18 <= user.Age && user.Age <= 32
```

## Const range

```
1..10_000
```

Ranges computed on the compile stage, are replaced with pre-created slices.

## Const expr

If some function is marked as a constant expression with `expr.ConstExpr`. It will be
replaced with the result of the call if all arguments are constants.

```go
expr.ConstExpt("fib")
```

```
fib(42)
``` 

Will be replaced with the result of `fib`(42)` on the compile step.

[ConstExpr Example](https://pkg.go.dev/github.com/antonmedv/expr?tab=doc#ConstExpr)

## Constant snapshots

Collection constants produced by constant folding, const range and const
expr are stored in the compiled program. As a caller may mutate the value
returned by a program run, Expr makes a copy of such constants on every
run. The copy is made lazily: only constants actually accessed during a
run are copied, and all references to the same constant within a single
run share the same copy. The copy preserves types, shared substructure
and cycles of the original value.

Constants which cannot be safely copied (values containing channels,
functions, unsafe pointers, or unexported struct fields with mutable
data) are rejected on the compile step.
