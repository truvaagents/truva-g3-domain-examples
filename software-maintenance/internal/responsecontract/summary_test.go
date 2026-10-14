package responsecontract

import "testing"

func TestSummaryProse(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"First paragraph.\n\nSecond paragraph: café.", true},
		{"Checks:\r\n\tpassed", true},
		{"A single line", true},
		{"", false}, {" \n\t", false}, {string([]byte{0xff}), false},
		{"a\x00b", false}, {"a\x1bb", false}, {"a\x7fb", false},
		{"a\u0085b", false}, {"a\vb", false}, {"a\fb", false},
	} {
		if got := ValidSummaryText(tc.value); got != tc.valid {
			t.Fatalf("summary %q: got %t, want %t", tc.value, got, tc.valid)
		}
	}
}

func TestSummaryLimitConfiguration(t *testing.T) {
	const key = "MAINTENANCE_TEST_MAXIMUM_SUMMARY_BYTES"
	for _, tc := range []struct {
		value   string
		present bool
		want    int
	}{
		{"", false, 10000}, {"10000", true, 10000}, {"4096", true, 4096},
		{"1", true, 1}, {"65536", true, 65536}, {"", true, 0},
		{"0", true, 0}, {"-1", true, 0}, {"1.5", true, 0},
		{"65537", true, 0}, {"many", true, 0}, {" 10000 ", true, 0},
	} {
		got, err := ResolveSummaryLimit(func(name string) (string, bool) {
			if name != key {
				t.Fatalf("unexpected key %q", name)
			}
			return tc.value, tc.present
		}, key)
		if got != tc.want || (err != nil) != (tc.want == 0) {
			t.Fatalf("%+v: got %d, %v", tc, got, err)
		}
	}
}
