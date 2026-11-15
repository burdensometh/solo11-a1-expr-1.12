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

## Per-evaluation limits

A compiled program is immutable and may be evaluated by many jobs. Everything
which belongs to a single evaluation (a cancellation signal and a work quota)
is passed to `expr.RunWithContext` / `vm.VM.RunWithLimits` and lives only in
the `VM` instance for the duration of that run. A run which is cancelled or
exceeds its quota returns an error bound to the expression location, and the
program remains usable by other jobs.

```go
program, _ := expr.Compile(`map(1..1000, {# * 2})`)

// Job A: stop after 500 units of work.
_, err := expr.RunWithContext(program, nil, vm.Limits{WorkBudget: 500})

// Job B: same program, its own cancellation and quota.
ctx, cancel := context.WithTimeout(ctx, time.Second)
defer cancel()
out, err := expr.RunWithContext(program, nil, vm.Limits{Context: ctx})
```

### Work metering

Work is measured, not estimated from the result: each executed instruction is
one unit, a range costs one unit per element when it is materialized, and a
collection constant pushed by `OpPush` (a const range, an array literal, the
map produced by the `in` optimization) costs one unit per element. This keeps
the optimized bytecode honest: without the last rule the optimizer could turn
`1..100000` into a single `OpPush` and the run would appear free.

### Cancellation

The job's context is observed at every collection loop iteration (`all`,
`any`, `one`, `none`, `filter`, `map`, `count`, and nested predicates), before
each range construction, before each host function call, and every 256
instructions for straight-line code. Host functions accepting a
`context.Context` as their first parameter receive the context of the current
run (see `expr.WithContext`); the context is resolved per run and never stored
in the program.

