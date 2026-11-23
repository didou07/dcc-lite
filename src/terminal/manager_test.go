package terminal

import "testing"

func TestValidateDimensions(t *testing.T) {
	good := [][2]int{{80, 24}, {20, 5}, {240, 100}}
	bad := [][2]int{{19, 24}, {241, 24}, {80, 4}, {80, 101}, {0, 0}, {-1, 24}}
	for _, d := range good {
		if err := ValidateDimensions(d[0], d[1]); err != nil {
			t.Errorf("%v rejected: %v", d, err)
		}
	}
	for _, d := range bad {
		if ValidateDimensions(d[0], d[1]) == nil {
			t.Errorf("%v accepted", d)
		}
	}
}

func TestAttachedStartsAtZeroAndManagerCloses(t *testing.T) {
	m := NewManager()
	if n := m.Attached(); n != 0 {
		t.Fatalf("Attached() = %d, want 0", n)
	}
	m.CloseAll()
}
