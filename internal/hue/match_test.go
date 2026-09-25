package hue

import (
	"errors"
	"strings"
	"testing"
)

func TestMatchLight(t *testing.T) {
	mk := func(id, name string) Light {
		var l Light
		l.ID, l.Metadata.Name = id, name
		return l
	}
	lights := []Light{
		mk("11111111-aaaa", "Kitchen"),
		mk("22222222-bbbb", "Desk lamp"),
		mk("33333333-cccc", "Desk plug"),
		mk("44444444-dddd", "desk"), // exact name that is also a prefix of others
	}
	cases := []struct {
		query, wantID, wantErr string
	}{
		{"11111111-aaaa", "11111111-aaaa", ""}, // exact id
		{"kitchen", "11111111-aaaa", ""},       // exact name, case-insensitive
		{"KITCH", "11111111-aaaa", ""},         // unique name prefix
		{"desk", "44444444-dddd", ""},          // exact name wins over prefix matches
		{"desk l", "22222222-bbbb", ""},        // unique prefix with a space
		{"desk p", "33333333-cccc", ""},        // unique prefix
		{"2222", "22222222-bbbb", ""},          // id prefix, 4 chars
		{"des", "", "ambiguous"},               // several names start with "des"
		{"garage", "", "no match"},             // nothing
		{"1", "", "no match"},                  // id prefix too short, no name starts with 1
		{"  ", "", "empty"},                    // blank
	}
	for _, c := range cases {
		got, err := MatchLight(lights, c.query)
		switch {
		case c.wantErr == "" && (err != nil || got.ID != c.wantID):
			t.Errorf("Match(%q) = %q, %v; want %q", c.query, got.ID, err, c.wantID)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("Match(%q) err = %v; want it to mention %q", c.query, err, c.wantErr)
		}
	}
	if _, err := MatchLight(lights, "nothing"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("no-match error should wrap ErrNoMatch, got %v", err)
	}
}
