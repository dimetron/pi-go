package routing

import "testing"

func TestFindPathStraightLine(t *testing.T) {
	isFree := func(col, row int) bool { return true }
	path := FindPath(0, 5, 9, 5, isFree, nil)
	if len(path) == 0 {
		t.Fatal("expected a path, got empty")
	}
	if path[0] != (Point{0, 5}) {
		t.Errorf("expected start at (0,5), got %v", path[0])
	}
	if path[len(path)-1] != (Point{9, 5}) {
		t.Errorf("expected end at (9,5), got %v", path[len(path)-1])
	}
}

func TestFindPathAroundObstacle(t *testing.T) {
	blocked := map[Point]bool{}
	for col := 3; col <= 7; col++ {
		blocked[Point{col, 5}] = true
	}
	isFree := func(col, row int) bool {
		if col < 0 || row < 0 || col >= 10 || row >= 10 {
			return false
		}
		return !blocked[Point{col, row}]
	}

	path := FindPath(0, 5, 9, 5, isFree, nil)
	if len(path) == 0 {
		t.Fatal("expected a path around obstacle, got empty")
	}
	if path[len(path)-1] != (Point{9, 5}) {
		t.Errorf("expected end at (9,5), got %v", path[len(path)-1])
	}
}

func TestSimplifyPath(t *testing.T) {
	path := []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	simplified := SimplifyPath(path)
	if len(simplified) != 2 {
		t.Errorf("expected 2 points after simplification, got %d: %v", len(simplified), simplified)
	}
}

func TestSimplifyPathWithCorner(t *testing.T) {
	path := []Point{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}}
	simplified := SimplifyPath(path)
	if len(simplified) != 3 {
		t.Errorf("expected 3 points (start, corner, end), got %d: %v", len(simplified), simplified)
	}
}
