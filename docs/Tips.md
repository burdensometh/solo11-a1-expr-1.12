# Tips

## Reuse VM

It is possible to reuse a virtual machine between re-runs on the program.
In some cases, it can add a small increase in performance (~10%).

```go
package main

import (
	"fmt"
	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/vm"
)

func main() {
	env := map[string]interface{}{
		"foo": 1,
		"bar": 2,
	}

	program, err := expr.Compile("foo + bar", expr.Env(env))
	if err != nil {
		panic(err)
	}

	// Reuse this vm instance between runs
	v := vm.VM{}

	out, err := v.Run(program, env)
	if err != nil {
		panic(err)
	}

	fmt.Print(out)
}
```

## Witnesses for all/any predicates

To explain why an `all` or `any` collection rule passed or failed, compile
the program with `expr.Witness()` and run it with `expr.RunWithWitness`.
Each executed `all`/`any` produces a `*vm.Witness` describing the element
that decided the result:

* `all` records the first counterexample;
* `any` records the first hit;
* an empty collection, or a predicate satisfied by every element, is
  represented by a witness without an element (`HasElement == false`,
  `Index == -1`).

A witness carries the source location of the predicate, the index of the
deciding element in the original collection and, for nested `all`/`any`
predicates, the inner witness chain. Witnesses are recorded only from
predicates that are really evaluated — nothing is recomputed to build an
explanation.

```go
program, err := expr.Compile(`all(1..4, {# < 3})`, expr.Witness())
if err != nil {
    panic(err)
}

out, witnesses, err := expr.RunWithWitness(program, nil)
if err != nil {
    panic(err)
}

fmt.Println(out)                 // false
fmt.Println(witnesses[0].Name)   // all
fmt.Println(witnesses[0].Index)  // 2 (the value 3 is the first counterexample)
```

Without `expr.Witness()` the compiled bytecode is unchanged and the
ordinary `expr.Run` API behaves exactly as before.
