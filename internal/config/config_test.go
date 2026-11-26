package config

import "testing"

func TestValidateListen(t *testing.T) {
	cases := []struct {
		listen, domain string
		want           string // the Listen after validate; "" means an error
	}{
		{"", "", ListenLocalhost},
		{"", "example.com", ListenLocalhost},
		{ListenLocalhost, "example.com", ListenLocalhost},
		{ListenTailnet, "", ListenTailnet},
		{ListenAll, "example.com", ListenAll},
		{ListenTailnet, "example.com", ""},
		{"0.0.0.0", "", ""},
		{"public", "", ""},
	}
	for _, c := range cases {
		cfg := Config{Listen: c.listen, Domain: c.domain}
		err := cfg.validate()
		if c.want == "" {
			if err == nil {
				t.Errorf("listen=%q domain=%q: want an error, got none", c.listen, c.domain)
			}
			continue
		}
		if err != nil {
			t.Errorf("listen=%q domain=%q: %v", c.listen, c.domain, err)
		} else if cfg.Listen != c.want {
			t.Errorf("listen=%q domain=%q: Listen = %q, want %q", c.listen, c.domain, cfg.Listen, c.want)
		}
	}
}
