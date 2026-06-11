package ui

import (
	"sort"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	in := []string{
		"app:setting:100",
		"app:setting:0",
		"app:setting:11",
		"app:setting:2",
		"app:setting:1",
	}
	sort.Slice(in, func(i, j int) bool { return NaturalLess(in[i], in[j]) })
	want := []string{
		"app:setting:0",
		"app:setting:1",
		"app:setting:2",
		"app:setting:11",
		"app:setting:100",
	}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("position %d: got %q, want %q (full: %v)", i, in[i], want[i], in)
		}
	}
}

func TestNaturalLessEdges(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"a2", "a10", true},
		{"a10", "a2", false},
		{"a02", "a2", false}, // numerically equal — compares equal both ways
		{"a2", "a02", false},
		{"abc", "abd", true},
		{"v1.0.9", "v1.0.10", true},
		{"same", "same", false},
		{"x", "x1", true},
		{"10", "9", false},
	}
	for _, c := range cases {
		if got := NaturalLess(c.a, c.b); got != c.want {
			t.Errorf("NaturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
