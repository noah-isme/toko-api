package tenant

import "testing"

func TestLocation(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"Asia/Jakarta", "Asia/Jakarta", true},
		{" Asia/Makassar ", "Asia/Makassar", true},
		{"UTC", "UTC", true},
		{"", "UTC", false},
		{"Mars/Phobos", "UTC", false},
	}
	for _, tc := range cases {
		loc, ok := Location(tc.name)
		if loc.String() != tc.want || ok != tc.wantOK {
			t.Errorf("Location(%q) = (%s, %v), want (%s, %v)", tc.name, loc, ok, tc.want, tc.wantOK)
		}
	}
}
