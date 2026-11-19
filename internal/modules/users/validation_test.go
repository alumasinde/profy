package users

import "testing"

func ptr(s string) *string { return &s }

func TestNormalizeNamesValid(t *testing.T) {
	cases := []struct {
		first  string
		middle *string
		last   string
		want   string
	}{
		{"Wanjiru", nil, "Kamau", "Wanjiru Kamau"},
		{"  Achieng’ ", ptr(""), " Odhiambo-Otieno", "Achieng’ Odhiambo-Otieno"},
		{"Jean-Luc", ptr("  Mwangi "), "O'Neil", "Jean-Luc Mwangi O'Neil"},
		{"Zoë", ptr("Ñandú"), "Müller", "Zoë Ñandú Müller"},
		{"मीना", nil, "शर्मा", "मीना शर्मा"},
		{"mary ann", nil, "de la cruz", "mary ann de la cruz"}, // case is never changed
		{"J.", nil, "Otieno", "J. Otieno"},
	}
	for _, c := range cases {
		n, errs := NormalizeNames(c.first, c.middle, c.last)
		if len(errs) != 0 {
			t.Errorf("%q %q: unexpected errors %v", c.first, c.last, errs)
			continue
		}
		if got := n.Display(); got != c.want {
			t.Errorf("Display() = %q, want %q", got, c.want)
		}
	}
}

func TestNormalizeNamesMiddleEmptyBecomesNil(t *testing.T) {
	n, _ := NormalizeNames("A", ptr("   "), "B")
	if n.Middle != nil {
		t.Fatal("blank middle name must be stored as NULL")
	}
}

func TestNormalizeNamesInvalid(t *testing.T) {
	long := ""
	for i := 0; i < 101; i++ {
		long += "a"
	}
	cases := []struct {
		first, last string
		middle      *string
		field, code string
	}{
		{"", "Kamau", nil, "first_name", "required"},
		{"Wanjiru", "   ", nil, "last_name", "required"},
		{"Wanj1ru", "Kamau", nil, "first_name", "invalid"},
		{"<b>Jo</b>", "Kamau", nil, "first_name", "invalid"},
		{"Jo\x00", "Kamau", nil, "first_name", "invalid"},
		{"Jo​", "Kamau", nil, "first_name", "invalid"},
		{"Jo  Ann", "Kamau", nil, "first_name", "invalid"},
		{"--", "Kamau", nil, "first_name", "invalid"},
		{long, "Kamau", nil, "first_name", "too_long"},
		{"Jo", "Kamau", ptr("M1"), "middle_name", "invalid"},
	}
	for _, c := range cases {
		_, errs := NormalizeNames(c.first, c.middle, c.last)
		if errs[c.field] != c.code {
			t.Errorf("NormalizeNames(%q,%v,%q)[%s] = %q, want %q", c.first, c.middle, c.last, c.field, errs[c.field], c.code)
		}
	}
}
