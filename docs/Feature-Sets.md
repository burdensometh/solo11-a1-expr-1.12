# Feature Sets

The `feature` package builds named, interdependent expression features on top
of expr. A **feature set** is a collection of named expressions that may
reference each other; the package works out the dependency graph and evaluates
every feature in dependency order.

```go
import (
	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/feature"
)
```

## Define and compile

Each feature has a name and an expression. Expressions may reference:

* **input fields** — names provided by the environment (see `expr.Env`);
* **feature references** — names of other features in the same set;
* **closure locals** — the pointer `#` inside closures, which is never
  treated as a reference.

The three kinds of names are distinguished at compile time, so the
configuration order does not matter: if C depends on B and B depends on A,
the set computes A, then B, then C, no matter how the features were listed.

```go
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
```

All `expr` options (`expr.Env`, `expr.Function`, `expr.Operator`, ...) apply
to every feature expression.

An input field always wins over a feature of the same name: a reference to a
name that exists in the environment is an input field and never creates a
dependency, even if a feature happens to share the name.

## Cycles

Cyclic dependencies are rejected at compile time with the full dependency
path:

```go
_, err := feature.Compile([]feature.Feature{
	{Name: "a", Expr: `c + 1`},
	{Name: "b", Expr: `a + 1`},
	{Name: "c", Expr: `b + 1`},
})
// err: feature: circular dependency detected: a -> c -> b -> a

var cycle *feature.CycleError
if errors.As(err, &cycle) {
	fmt.Println(cycle.Path) // [a c b a]
}
```

## Evaluate

`Run` evaluates every feature once, shares the computed values with all
dependents, and returns a result — a value and a status — for each feature:

```go
results := set.Run(map[string]interface{}{
	"price":  10.0,
	"amount": 2,
	"rate":   0.2,
})

results["gross"].Value  // 24
results["gross"].Status // feature.StatusOK
```

A feature that fails only blocks its dependent branch. Dependents
(transitively) are reported as `StatusBlocked`; independent features are
still evaluated and return their values and statuses:

```go
results := set.Run(env)
for _, name := range set.EvaluationOrder() {
	r := results[name]
	switch r.Status {
	case feature.StatusOK:
		fmt.Printf("%s = %v\n", name, r.Value)
	case feature.StatusError:
		fmt.Printf("%s failed: %v\n", name, r.Err)
	case feature.StatusBlocked:
		fmt.Printf("%s blocked: %v\n", name, r.Err)
	}
}
```

## Introspection

```go
set.Names()               // feature names in definition order
set.EvaluationOrder()     // topological evaluation order
set.Dependencies("gross") // ["net", "vat"]
set.Inputs("gross")       // input fields referenced by the expression
```

A compiled `Set` is immutable and safe for concurrent use.
