package feature

// topoSort orders feature names so that every feature comes after all of
// its dependencies, regardless of the order in which the features were
// defined. Independent features keep their relative definition order, so the
// result is deterministic. A cyclic graph is reported as a *CycleError
// carrying the full dependency path of the cycle.
func topoSort(names []string, deps map[string][]string) ([]string, error) {
	if err := findCycle(names, deps); err != nil {
		return nil, err
	}

	// Kahn's algorithm with a first-in-first-out queue seeded in definition
	// order.
	indegree := make(map[string]int, len(names))
	dependents := make(map[string][]string, len(names))
	for _, name := range names {
		indegree[name] = len(deps[name])
		for _, dep := range deps[name] {
			dependents[dep] = append(dependents[dep], name)
		}
	}

	var queue []string
	for _, name := range names {
		if indegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	order := make([]string, 0, len(names))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)
		for _, dependent := range dependents[name] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	return order, nil
}

// findCycle performs a depth-first search over the dependency graph and
// returns a *CycleError with the full path of the first cycle found, or nil
// if the graph is acyclic.
func findCycle(names []string, deps map[string][]string) error {
	const (
		white = iota // unvisited
		gray         // on the current path
		black        // fully explored
	)
	color := make(map[string]int, len(names))
	var stack []string

	var visit func(name string) *CycleError
	visit = func(name string) *CycleError {
		color[name] = gray
		stack = append(stack, name)
		for _, dep := range deps[name] {
			switch color[dep] {
			case gray:
				// dep is on the current path: the cycle is the stack
				// segment from dep to the top, closed by dep itself.
				start := 0
				for start < len(stack) && stack[start] != dep {
					start++
				}
				path := append([]string(nil), stack[start:]...)
				path = append(path, dep)
				return &CycleError{Path: path}
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return nil
	}

	for _, name := range names {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}
