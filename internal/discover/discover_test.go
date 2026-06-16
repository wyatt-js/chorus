package discover

import "testing"

func TestFriendlyName(t *testing.T) {
	tests := []struct {
		instance string
		want     string
	}{
		{`AABBCCDDEEFF@Living Room`, "Living Room"},
		{`AABBCCDDEEFF@Wyatt\226\128\153s\ MacBook\ Air`, "Wyatt’s MacBook Air"},
		{`No At Sign`, "No At Sign"},
		{`AABBCC@Den\ Speaker`, "Den Speaker"},
	}
	for _, tt := range tests {
		if got := friendlyName(tt.instance); got != tt.want {
			t.Errorf("friendlyName(%q) = %q, want %q", tt.instance, got, tt.want)
		}
	}
}

func TestParseTXT(t *testing.T) {
	m := parseTXT([]string{"et=0,1", "cn=0,1", "flag"})
	if m["et"] != "0,1" || m["cn"] != "0,1" {
		t.Errorf("parseTXT values wrong: %v", m)
	}
	if _, ok := m["flag"]; !ok {
		t.Errorf("parseTXT dropped valueless key: %v", m)
	}
}
