package api

import "testing"

func TestAtoiU16(t *testing.T) {
	cases := []struct {
		in   string
		want uint16
	}{
		{"", 0},
		{"0", 0},
		{"24", 24},
		{"65535", 65535},
		{"65536", 0}, // overflow → 0
		{"-1", 0},    // negative → 0
		{"abc", 0},   // non-numeric → 0
		{"  12 ", 0}, // strconv.Atoi rejects surrounding spaces
		{"120", 120},
	}
	for _, c := range cases {
		if got := atoiU16(c.in); got != c.want {
			t.Errorf("atoiU16(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
