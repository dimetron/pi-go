package palace

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Path evaluation, after Tiramemsu's path-evaluation capability. A search
// walks entity-to-entity statements of one view inside one read snapshot and
// one budget meter.

// PathMode selects what a path search returns.
type PathMode int

const (
	// PathReach returns one path per reachable target: the one that arrives
	// earliest (time-respecting) or is shortest.
	PathReach PathMode = iota
	// PathShortest returns one shortest path per target.
	PathShortest
	// PathAllShortest returns every shortest path per target.
	PathAllShortest
	// PathTrails returns every trail (no repeated statement) within the hop
	// limits, shortest first, up to Limit.
	PathTrails
)

// String names the mode as ParsePathMode accepts it.
func (m PathMode) String() string {
	switch m {
	case PathReach:
		return "reach"
	case PathShortest:
		return "shortest"
	case PathAllShortest:
		return "all_shortest"
	case PathTrails:
		return "trails"
	}
	return fmt.Sprintf("PathMode(%d)", int(m))
}

// ParsePathMode parses "reach", "shortest", "all_shortest" or "trails".
func ParsePathMode(s string) (PathMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "reach":
		return PathReach, nil
	case "shortest":
		return PathShortest, nil
	case "all_shortest", "all-shortest", "allshortest":
		return PathAllShortest, nil
	case "trails", "trail":
		return PathTrails, nil
	}
	return 0, fmt.Errorf("palace: kg: unknown path mode %q (want reach, shortest, all_shortest or trails)", s)
}

const (
	defaultPathMinHops  = 1
	defaultPathMaxHops  = 6
	maxPathHops         = 16
	defaultPathLimit    = 50
	defaultPathFrontier = 100000
)

// PathQuery describes a path search.
type PathQuery struct {
	// From and To are entity names; at least one is required. With only To
	// the search runs backwards and paths still read From-side first.
	From, To string
	// Predicates restricts the edges; empty is any non-engine predicate.
	Predicates []string
	// Direction is "out" (subject to object, the default), "in" or "both".
	Direction string
	// MinHops and MaxHops bound the path length: default 1..6, MaxHops capped
	// at 16.
	MinHops, MaxHops int
	Mode             PathMode
	// TimeRespecting requires each edge to still be valid when the walk
	// reaches it: it carries the arrival instant and waits when it must.
	TimeRespecting bool
	// Limit bounds the paths returned (default 50); MaxFrontier bounds the
	// search states held (default 100000) and fails with ErrBudget.
	Limit       int
	MaxFrontier int
	Budget      Budget
}

// PathStep is one traversed statement, in its own orientation.
type PathStep struct {
	EID       int64
	From, To  Value
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// Path is one result: Nodes[i] and Nodes[i+1] are joined by Steps[i].
type Path struct {
	Nodes []Value
	Steps []PathStep
	// Arrival is the instant the walk reached the last node when the search
	// was time-respecting; nil when unbounded.
	Arrival *time.Time
}

// pathEdge is one traversable edge from a node.
type pathEdge struct {
	step       PathStep
	next       Value
	vFrom, vTo int64
}

const (
	tauNegInf = math.MinInt64
	tauPosInf = math.MaxInt64
)

// pathLink is how a state was reached: from states[parent] over edge.
type pathLink struct {
	parent int
	edge   *pathEdge
}

// pathState is one search state; links lead back to the start. Only
// AllShortest keeps more than one.
type pathState struct {
	node  Value
	hops  int
	hk    int
	tau   int64
	links []pathLink
}

type pathKey struct {
	node Value
	hk   int
	tau  int64
}

type pathSearch struct {
	v      View
	q      querier
	pq     PathQuery
	m      *meter
	dir    string // effective direction
	preds  []Value
	cache  map[Value][]pathEdge
	states []pathState
	start  Value
}

// Paths searches the view for paths per pq.
func (v View) Paths(ctx context.Context, pq PathQuery) ([]Path, error) {
	if pq.From == "" && pq.To == "" {
		return nil, fmt.Errorf("palace: kg: path search needs from or to")
	}
	if pq.From == "" && pq.TimeRespecting {
		return nil, fmt.Errorf("palace: kg: a time-respecting path search needs from; searching back from to alone is not supported")
	}
	if pq.MinHops < 0 || pq.MaxHops < 0 || pq.Limit < 0 || pq.MaxFrontier < 0 {
		return nil, fmt.Errorf("palace: kg: path limits must not be negative")
	}
	if pq.MinHops == 0 {
		pq.MinHops = defaultPathMinHops
	}
	if pq.MaxHops == 0 {
		pq.MaxHops = defaultPathMaxHops
	}
	pq.MaxHops = min(pq.MaxHops, maxPathHops)
	if pq.MinHops > pq.MaxHops {
		return nil, fmt.Errorf("palace: kg: min hops %d exceeds max hops %d", pq.MinHops, pq.MaxHops)
	}
	if pq.Limit == 0 {
		pq.Limit = defaultPathLimit
	}
	if pq.MaxFrontier == 0 {
		pq.MaxFrontier = defaultPathFrontier
	}
	dir := pq.Direction
	switch dir {
	case "", "out":
		dir = "out"
	case "in", "both":
	default:
		return nil, fmt.Errorf("palace: kg: unknown path direction %q (want out, in or both)", pq.Direction)
	}
	backward := pq.From == ""
	eff := dir
	if backward {
		switch dir {
		case "out":
			eff = "in"
		case "in":
			eff = "out"
		}
	}
	s := &pathSearch{v: v, pq: pq, dir: eff, cache: map[Value][]pathEdge{}}
	for _, p := range pq.Predicates {
		s.preds = append(s.preds, Ent(p))
	}
	var out []Path
	err := v.kg.read(ctx, func(q querier) error {
		s.q = q
		s.m = newMeter(ctx, pq.Budget)
		var err error
		out, err = s.run(ctx, backward)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// edges lists the traversable edges at node, in eid order, loading once.
func (s *pathSearch) edges(ctx context.Context, node Value) ([]pathEdge, error) {
	if es, ok := s.cache[node]; ok {
		return es, nil
	}
	var sts []Statement
	load := func(bySubject bool) error {
		pats := []Pattern{{}}
		if len(s.preds) > 0 {
			pats = pats[:0]
			for i := range s.preds {
				pats = append(pats, Pattern{Predicate: &s.preds[i]})
			}
		}
		for _, p := range pats {
			if bySubject {
				p.Subject = &node
			} else {
				p.Object = &node
			}
			got, err := s.v.match(ctx, s.q, p)
			if err != nil {
				return err
			}
			if err := s.m.takeStatements(got); err != nil {
				return err
			}
			sts = append(sts, got...)
		}
		return nil
	}
	if s.dir == "out" || s.dir == "both" {
		if err := load(true); err != nil {
			return nil, err
		}
	}
	if s.dir == "in" || s.dir == "both" {
		if err := load(false); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(sts, func(a, b Statement) int { return cmp.Compare(a.EID, b.EID) })
	sts = slices.CompactFunc(sts, func(a, b Statement) bool { return a.EID == b.EID })
	es := make([]pathEdge, 0, len(sts))
	for _, st := range sts {
		if st.Subject.Kind != KindEntity || st.Object.Kind != KindEntity {
			continue
		}
		e := pathEdge{
			step:  PathStep{EID: st.EID, From: st.Subject, To: st.Object, ValidFrom: st.ValidFrom, ValidTo: st.ValidTo},
			vFrom: tauNegInf, vTo: tauPosInf,
		}
		if st.ValidFrom != nil {
			e.vFrom = st.ValidFrom.UnixMilli()
		}
		if st.ValidTo != nil {
			e.vTo = st.ValidTo.UnixMilli()
		}
		switch {
		case s.dir != "in" && st.Subject == node:
			e.next = st.Object
		case s.dir != "out" && st.Object == node:
			e.next = st.Subject
		default:
			continue
		}
		es = append(es, e)
	}
	s.cache[node] = es
	return es, nil
}

func (s *pathSearch) add(st pathState) (int, error) {
	if len(s.states) >= s.pq.MaxFrontier {
		return 0, fmt.Errorf("%w: more than %d search states", ErrBudget, s.pq.MaxFrontier)
	}
	s.states = append(s.states, st)
	return len(s.states) - 1, nil
}

func (s *pathSearch) run(ctx context.Context, backward bool) ([]Path, error) {
	s.start = Ent(s.pq.From)
	var target Value
	hasTarget := s.pq.To != "" && !backward
	if hasTarget {
		target = Ent(s.pq.To)
	}
	if backward {
		s.start = Ent(s.pq.To)
	}
	var found []int
	var err error
	if s.pq.Mode == PathTrails {
		found, err = s.trails(ctx, target, hasTarget)
	} else {
		found, err = s.bfs(ctx, target, hasTarget)
	}
	if err != nil {
		return nil, err
	}
	var out []Path
	for _, i := range found {
		out = append(out, s.build(i, backward)...)
	}
	slices.SortStableFunc(out, func(a, b Path) int {
		return cmpEIDs(a.Steps, b.Steps)
	})
	if len(out) > s.pq.Limit {
		out = out[:s.pq.Limit]
	}
	return out, nil
}

// cmpEIDs orders paths by length, then by eid sequence.
func cmpEIDs(a, b []PathStep) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	for i := range a {
		if c := cmp.Compare(a[i].EID, b[i].EID); c != 0 {
			return c
		}
	}
	return 0
}

// usable reports whether e can be taken at arrival tau, and the new arrival.
func (s *pathSearch) usable(tau int64, e *pathEdge) (int64, bool) {
	if !s.pq.TimeRespecting {
		return 0, true
	}
	if e.vTo != tauPosInf && e.vTo <= tau {
		return 0, false
	}
	return max(tau, e.vFrom), true
}

// bfs explores layer by layer over deduplicated states and returns the
// indexes of the chosen end states.
func (s *pathSearch) bfs(ctx context.Context, target Value, hasTarget bool) ([]int, error) {
	t0 := int64(0)
	if s.pq.TimeRespecting {
		t0 = tauNegInf
	}
	root, err := s.add(pathState{node: s.start, tau: t0})
	if err != nil {
		return nil, err
	}
	seen := map[pathKey]int{{s.start, 0, t0}: root}
	layer := []int{root}
	var ends []int
	for hops := 0; hops < s.pq.MaxHops && len(layer) > 0; hops++ {
		var next []int
		for _, si := range layer {
			if err := s.m.check(); err != nil {
				return nil, err
			}
			cur := s.states[si]
			es, err := s.edges(ctx, cur.node)
			if err != nil {
				return nil, err
			}
			for i := range es {
				e := &es[i]
				tau, ok := s.usable(cur.tau, e)
				if !ok {
					continue
				}
				k := pathKey{e.next, min(hops+1, s.pq.MinHops), tau}
				if j, dup := seen[k]; dup {
					if s.pq.Mode == PathAllShortest && s.states[j].hops == hops+1 {
						s.states[j].links = append(s.states[j].links, pathLink{si, e})
					}
					continue
				}
				j, err := s.add(pathState{node: e.next, hops: hops + 1, hk: k.hk, tau: tau,
					links: []pathLink{{si, e}}})
				if err != nil {
					return nil, err
				}
				seen[k] = j
				next = append(next, j)
			}
		}
		layer = next
		for _, j := range next {
			st := s.states[j]
			// Without an explicit To the start is not its own target.
			if st.hk == s.pq.MinHops && ((hasTarget && st.node == target) || (!hasTarget && st.node != s.start)) {
				ends = append(ends, j)
			}
		}
	}
	return s.choose(ends), nil
}

// firstEIDs is the eid sequence of a state's first-link walk.
func (s *pathSearch) firstEIDs(i int) []int64 {
	var rev []int64
	for st := s.states[i]; len(st.links) > 0; st = s.states[st.links[0].parent] {
		rev = append(rev, st.links[0].edge.step.EID)
	}
	slices.Reverse(rev)
	return rev
}

// choose picks the end states per target by mode. Reach prefers the earliest
// arrival then the shortest; Shortest the shortest then the earliest;
// AllShortest keeps every state of the shortest length.
func (s *pathSearch) choose(ends []int) []int {
	byNode := map[Value][]int{}
	var order []Value
	for _, i := range ends {
		n := s.states[i].node
		if _, ok := byNode[n]; !ok {
			order = append(order, n)
		}
		byNode[n] = append(byNode[n], i)
	}
	var out []int
	for _, n := range order {
		group := byNode[n]
		minHops := s.states[group[0]].hops
		for _, i := range group {
			minHops = min(minHops, s.states[i].hops)
		}
		if s.pq.Mode == PathAllShortest {
			for _, i := range group {
				if s.states[i].hops == minHops {
					out = append(out, i)
				}
			}
			continue
		}
		best := -1
		for _, i := range group {
			if best < 0 || s.better(i, best) {
				best = i
			}
		}
		out = append(out, best)
	}
	return out
}

func (s *pathSearch) better(a, b int) bool {
	sa, sb := s.states[a], s.states[b]
	var c int
	if s.pq.Mode == PathReach {
		c = cmp.Compare(sa.tau, sb.tau)
		if c == 0 {
			c = cmp.Compare(sa.hops, sb.hops)
		}
	} else {
		c = cmp.Compare(sa.hops, sb.hops)
		if c == 0 {
			c = cmp.Compare(sa.tau, sb.tau)
		}
	}
	if c == 0 {
		c = slices.Compare(s.firstEIDs(a), s.firstEIDs(b))
	}
	return c < 0
}

// trails enumerates edge-simple walks breadth first, so results come
// shortest first, and stops after the layer that reaches Limit.
func (s *pathSearch) trails(ctx context.Context, target Value, hasTarget bool) ([]int, error) {
	t0 := int64(0)
	if s.pq.TimeRespecting {
		t0 = tauNegInf
	}
	root, err := s.add(pathState{node: s.start, tau: t0})
	if err != nil {
		return nil, err
	}
	layer := []int{root}
	var ends []int
	for hops := 0; hops < s.pq.MaxHops && len(layer) > 0 && len(ends) < s.pq.Limit; hops++ {
		var next []int
		for _, si := range layer {
			if err := s.m.check(); err != nil {
				return nil, err
			}
			cur := s.states[si]
			es, err := s.edges(ctx, cur.node)
			if err != nil {
				return nil, err
			}
			for i := range es {
				e := &es[i]
				if s.usesEdge(si, e.step.EID) {
					continue
				}
				tau, ok := s.usable(cur.tau, e)
				if !ok {
					continue
				}
				j, err := s.add(pathState{node: e.next, hops: hops + 1, tau: tau, links: []pathLink{{si, e}}})
				if err != nil {
					return nil, err
				}
				next = append(next, j)
				if hops+1 >= s.pq.MinHops && (!hasTarget || e.next == target) {
					ends = append(ends, j)
				}
			}
		}
		layer = next
	}
	return ends, nil
}

func (s *pathSearch) usesEdge(i int, eid int64) bool {
	for st := s.states[i]; len(st.links) > 0; st = s.states[st.links[0].parent] {
		if st.links[0].edge.step.EID == eid {
			return true
		}
	}
	return false
}

// build turns an end state into its paths: one, or every shortest walk for
// AllShortest (at most Limit).
func (s *pathSearch) build(i int, backward bool) []Path {
	var walks [][]*pathEdge
	var rec func(i int, tail []*pathEdge)
	rec = func(i int, tail []*pathEdge) {
		if len(walks) >= s.pq.Limit {
			return
		}
		st := s.states[i]
		if len(st.links) == 0 {
			w := slices.Clone(tail)
			slices.Reverse(w)
			walks = append(walks, w)
			return
		}
		links := st.links
		if s.pq.Mode != PathAllShortest {
			links = links[:1]
		}
		for _, l := range links {
			rec(l.parent, append(tail, l.edge))
		}
	}
	rec(i, nil)
	var arrival *time.Time
	if s.pq.TimeRespecting && s.states[i].tau != tauNegInf {
		t := time.UnixMilli(s.states[i].tau).UTC()
		arrival = &t
	}
	out := make([]Path, 0, len(walks))
	for _, w := range walks {
		p := Path{Nodes: []Value{s.start}, Arrival: arrival}
		for _, e := range w {
			p.Steps = append(p.Steps, e.step)
			p.Nodes = append(p.Nodes, e.next)
		}
		if backward {
			slices.Reverse(p.Nodes)
			slices.Reverse(p.Steps)
		}
		out = append(out, p)
	}
	return out
}
