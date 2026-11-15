// Package feature provides named, interdependent expression features built on
// top of the expr package.
//
// A Set is a collection of named features, each defined by an expr
// expression. Expressions may reference three kinds of names, which are
// distinguished at compile time:
//
//   - input fields: names provided by the environment (see expr.Env);
//   - feature references: names of other features in the same set;
//   - closure locals: the pointer (#) inside closures such as
//     filter(list, {# > limit}), which is never treated as a reference.
//
// Features are evaluated in dependency order, not configuration order: if C
// depends on B and B depends on A, the set computes A, then B, then C no
// matter how the features were listed. Every feature is computed at most once
// per run and its value is shared by all dependents. Cycles are rejected at
// compile time with the full dependency path.
//
// A feature that fails at run time only blocks its dependent branch:
// dependents (transitively) are reported as blocked, while independent
// features still return their values and statuses.
package feature

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/antonmedv/expr"
	"github.com/antonmedv/expr/conf"
	"github.com/antonmedv/expr/parser"
	"github.com/antonmedv/expr/vm"
)

// Feature is a single named expression within a set.
type Feature struct {
	// Name uniquely identifies the feature. It must be a valid expr
	// identifier and must not collide with a keyword, a builtin, or a
	// function name.
	Name string
	// Expr is the expression computing the feature's value. It may
	// reference input fields and other features of the set by name.
	Expr string
}

// Status describes the outcome of a single feature evaluation.
type Status int

const (
	// StatusOK means the feature was evaluated successfully.
	StatusOK Status = iota
	// StatusError means the feature itself failed to evaluate.
	StatusError
	// StatusBlocked means the feature was not evaluated because one of its
	// dependencies failed or was blocked.
	StatusBlocked
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusError:
		return "error"
	case StatusBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// Result is the outcome of a single feature evaluation.
type Result struct {
	// Value is the computed value. It is set only when Status is StatusOK.
	Value interface{}
	// Status tells whether the feature was computed, failed, or blocked.
	Status Status
	// Err describes the failure (StatusError) or the blocking dependency
	// (StatusBlocked). It is nil when Status is StatusOK.
	Err error
}

// Results maps feature names to their evaluation results. Every feature of
// the set is present, whether it succeeded, failed, or was blocked.
type Results map[string]Result

// Value returns the computed value of a feature and whether it was
// evaluated successfully.
func (rs Results) Value(name string) (interface{}, bool) {
	r, ok := rs[name]
	if !ok || r.Status != StatusOK {
		return nil, false
	}
	return r.Value, true
}

// OK reports whether every feature of the set was evaluated successfully.
func (rs Results) OK() bool {
	for _, r := range rs {
		if r.Status != StatusOK {
			return false
		}
	}
	return true
}

// CycleError is returned by Compile when features depend on each other
// cyclically. Path contains the full cycle, e.g. [A, B, C, A] means A
// depends on B, B depends on C, and C depends back on A.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "feature: circular dependency detected: " + strings.Join(e.Path, " -> ")
}

// BlockedError describes a feature that was not evaluated because one of
// its dependencies did not produce a value.
type BlockedError struct {
	Feature    string
	Dependency string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("feature %q blocked: dependency %q failed", e.Feature, e.Dependency)
}

// Set is a compiled, dependency-ordered set of named features. A Set is
// immutable after Compile and safe for concurrent use.
type Set struct {
	names      []string            // definition order
	deps       map[string][]string // name -> direct feature dependencies (sorted)
	inputs     map[string][]string // name -> referenced input fields (sorted)
	order      []string            // topological evaluation order
	programs   map[string]*vm.Program
	inputNames map[string]bool // names provided by the environment, never shadowed
}

var (
	namePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reservedName = map[string]bool{
		// Keywords and literals of the expr language.
		"env": true, "true": true, "false": true, "nil": true,
		"not": true, "and": true, "or": true, "in": true,
		"matches": true, "contains": true, "startsWith": true, "endsWith": true,
		// Builtins are parsed as BuiltinNode and can never name a feature.
		"all": true, "none": true, "any": true, "one": true,
		"filter": true, "map": true, "count": true,
	}
)

// Compile validates, analyzes, and compiles a set of named features.
//
// The given expr options (expr.Env, expr.Function, expr.Operator, ...) are
// applied to every feature expression. Names declared by the environment
// always win over feature names: a reference to a name that exists in the
// environment is an input field and never creates a dependency, even if a
// feature happens to share the name.
//
// Compile fails if a feature name is invalid or duplicated, if an expression
// does not compile, or if the dependency graph contains a cycle; cycles are
// reported as *CycleError with the full dependency path.
func Compile(features []Feature, opts ...expr.Option) (*Set, error) {
	s := &Set{
		deps:     make(map[string][]string, len(features)),
		inputs:   make(map[string][]string, len(features)),
		programs: make(map[string]*vm.Program, len(features)),
	}

	// Validate names.
	seen := make(map[string]bool, len(features))
	for _, f := range features {
		if !namePattern.MatchString(f.Name) {
			return nil, fmt.Errorf("feature: invalid feature name %q", f.Name)
		}
		if reservedName[f.Name] {
			return nil, fmt.Errorf("feature: feature name %q is reserved", f.Name)
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("feature: duplicate feature name %q", f.Name)
		}
		seen[f.Name] = true
		s.names = append(s.names, f.Name)
	}

	// Probe the configuration to learn which names the environment provides
	// (input fields and functions) before any feature name is considered.
	probe := conf.CreateNew()
	for _, op := range opts {
		op(probe)
	}
	isFunc := make(map[string]bool, len(probe.Functions))
	for name := range probe.Functions {
		isFunc[name] = true
	}
	s.inputNames = make(map[string]bool, len(probe.Types))
	for name := range probe.Types {
		s.inputNames[name] = true
	}
	for _, name := range s.names {
		if isFunc[name] {
			return nil, fmt.Errorf("feature: feature name %q conflicts with a function of the same name", name)
		}
	}

	// extend makes feature references resolvable: every feature name that is
	// neither an input field nor a function is declared as an interface{}
	// variable, and code is generated for a map environment so the runtime
	// can supply both user inputs and computed feature values in one map.
	anyType := reflect.TypeOf(new(interface{})).Elem()
	extend := func(c *conf.Config) {
		if c.Types == nil {
			c.Types = conf.TypesTable{}
		}
		for _, name := range s.names {
			if _, ok := c.Types[name]; ok {
				continue // an input field with the same name takes precedence
			}
			if _, ok := c.Functions[name]; ok {
				continue
			}
			c.Types[name] = conf.Tag{Type: anyType}
		}
		c.MapEnv = true
	}
	compileOpts := make([]expr.Option, 0, len(opts)+1)
	compileOpts = append(compileOpts, opts...)
	compileOpts = append(compileOpts, extend)

	// Parse, analyze, and compile each feature.
	for _, f := range features {
		tree, err := parser.Parse(f.Expr)
		if err != nil {
			return nil, fmt.Errorf("feature %q: %w", f.Name, err)
		}
		s.deps[f.Name], s.inputs[f.Name] = analyze(tree.Node, s.inputNames, seen, isFunc)

		program, err := expr.Compile(f.Expr, compileOpts...)
		if err != nil {
			return nil, fmt.Errorf("feature %q: %w", f.Name, err)
		}
		s.programs[f.Name] = program
	}

	// Order features by dependency; reject cycles with their full path.
	order, err := topoSort(s.names, s.deps)
	if err != nil {
		return nil, err
	}
	s.order = order
	return s, nil
}

// Names returns the feature names in definition order.
func (s *Set) Names() []string {
	return append([]string(nil), s.names...)
}

// EvaluationOrder returns the order in which features are evaluated:
// a topological order of the dependency graph, independent of the order in
// which features were defined.
func (s *Set) EvaluationOrder() []string {
	return append([]string(nil), s.order...)
}

// Dependencies returns the names of the features the given feature directly
// depends on, sorted alphabetically. The second return value reports whether
// the feature exists.
func (s *Set) Dependencies(name string) ([]string, bool) {
	deps, ok := s.deps[name]
	return append([]string(nil), deps...), ok
}

// Inputs returns the names of the environment input fields the given
// feature's expression references, sorted alphabetically. The second return
// value reports whether the feature exists.
func (s *Set) Inputs(name string) ([]string, bool) {
	inputs, ok := s.inputs[name]
	return append([]string(nil), inputs...), ok
}

// Run evaluates every feature of the set in dependency order and returns a
// result for each of them.
//
// Computed feature values are shared: each feature is evaluated at most
// once, and its value is supplied to the evaluation of its dependents. If a
// feature fails, only that branch of the dependency graph is affected —
// dependents are reported as blocked, while independent features are still
// evaluated and return their values and statuses.
//
// The env argument accepts the same kinds of values as expr.Run (a map or a
// struct); it is viewed as a map of input fields for the evaluation. Input
// fields take precedence over feature values: a feature whose name collides
// with an input field never shadows that input.
func (s *Set) Run(env interface{}) Results {
	merged := toMap(env)
	results := make(Results, len(s.names))
	status := make(map[string]Status, len(s.names))

	for _, name := range s.order {
		// A feature is blocked if any direct dependency did not produce a
		// value; because evaluation follows the topological order, blocked
		// dependencies propagate transitively.
		if cause := failedDependency(s.deps[name], status); cause != "" {
			results[name] = Result{Status: StatusBlocked, Err: &BlockedError{Feature: name, Dependency: cause}}
			status[name] = StatusBlocked
			continue
		}

		out, err := vm.Run(s.programs[name], merged)
		if err != nil {
			results[name] = Result{Status: StatusError, Err: err}
			status[name] = StatusError
			continue
		}

		results[name] = Result{Status: StatusOK, Value: out}
		status[name] = StatusOK
		// Share the computed value with dependents. An input field of the
		// same name is never shadowed: references to it keep reading the
		// input, exactly as classified at compile time.
		if !s.inputNames[name] {
			merged[name] = out
		}
	}
	return results
}

// failedDependency returns the name of the first direct dependency that did
// not evaluate successfully, or "" if all dependencies are OK.
func failedDependency(deps []string, status map[string]Status) string {
	for _, dep := range deps {
		if status[dep] != StatusOK {
			return dep
		}
	}
	return ""
}

// sortedKeys returns the sorted keys of a set of names.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
