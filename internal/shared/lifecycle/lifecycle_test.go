package lifecycle

import "testing"

func TestPermission(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{Draft, Published, "pages.publish"},
		{Published, Draft, "pages.publish"},
		{Published, Archived, "pages.archive"},
		{Draft, Archived, "pages.archive"},
		{Archived, Draft, "pages.publish"},
		{Draft, Draft, "pages.update"},
	}
	for _, c := range cases {
		if got := Permission("pages", c.from, c.to); got != c.want {
			t.Errorf("%s→%s = %s, want %s", c.from, c.to, got, c.want)
		}
	}
}
