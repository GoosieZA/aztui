package version

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.6.0", "v0.5.0", true},
		{"v0.5.0", "v0.5.0", false},
		{"v0.5.0", "v0.6.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.5.1", "v0.5.0", true},
		{"v0.10.0", "v0.9.0", true}, // numeric, not lexical
		{"v0.6.0", "v0.5.0-dirty", true},
		{"garbage", "v0.5.0", false},
		{"v0.6.0", "garbage", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
