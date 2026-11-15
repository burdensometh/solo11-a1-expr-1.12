package vm

import "github.com/antonmedv/expr/file"

// Witness records the element that actually decided the result of an
// all/any collection predicate during a real program execution.
//
// A witness is captured only when the program was compiled with the
// witness option enabled. Predicates are never re-evaluated to build a
// witness; every field reflects an execution that really happened.
type Witness struct {
	// Name is the name of the collection builtin ("all" or "any").
	Name string

	// Location is the source location of the predicate expression
	// (the position of the builtin keyword in the source code).
	Location file.Location

	// HasElement reports whether the predicate was actually evaluated
	// against an element. An empty collection leaves the witness
	// without an element: HasElement is false and Index is -1.
	HasElement bool

	// Index is the index of the deciding element in the original
	// collection: the first counterexample for all, the first hit for
	// any. It is -1 when the collection was empty.
	Index int

	// Inner is the witness of the nested all/any predicate which
	// decided the outcome of this witness's element, if any.
	Inner *Witness
}

// WitnessInfo is stored among the program constants when a program is
// compiled with the witness option. It carries everything an
// OpWitnessPush instruction needs which is known at compile time.
type WitnessInfo struct {
	Name string
	Loc  file.Location
}

// witnessFrame accumulates the witness of a single all/any builtin
// while its loop is running.
type witnessFrame struct {
	name string
	loc  file.Location

	// hasElement and index describe the element which decided the
	// loop. For all this is the first counterexample, for any the
	// first hit. Both stay at their zero values when the collection
	// is empty.
	hasElement bool
	index      int

	// inner holds the latest completed witness produced by a nested
	// all/any builtin evaluated for the deciding element.
	inner *Witness
}

func (f *witnessFrame) toWitness() *Witness {
	w := &Witness{
		Name:       f.name,
		Location:   f.loc,
		HasElement: f.hasElement,
		Index:      -1,
	}
	if f.hasElement {
		w.Index = f.index
		// The nested chain only explains why the deciding element
		// evaluated the way it did.
		w.Inner = f.inner
	}
	return w
}
