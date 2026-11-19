package update

import "testing"

func TestNormalizeVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{"0.2.0", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", true},
		{"1.2.3-rc.1", "v1.2.3-rc.1", true},
		{"dev", "", false},
		{"1.2", "", false},
		{"", "", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizeVersion(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("NormalizeVersion() error = %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("NormalizeVersion() expected error, got %q", got)
			}
			if got != tc.want {
				t.Fatalf("NormalizeVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		current string
		target  string
		want    int
	}{
		{"0.2.0", "0.3.0", -1},
		{"0.3.0", "v0.3.0", 0},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
	} {
		got, err := CompareVersions(tc.current, tc.target)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q) error = %v", tc.current, tc.target, err)
		}
		if got != tc.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tc.current, tc.target, got, tc.want)
		}
	}
}
