package config

import "testing"

func TestVikunjaEndpointIdentity(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{" HTTPS://Vikunja.Example:443/prefix/ ", "https://vikunja.example/prefix"},
		{"http://192.168.1.10:3456", "http://192.168.1.10:3456"},
		{"http://[0:0:0:0:0:0:0:1]:80/prefix", "http://[::1]/prefix"},
	} {
		got, err := NormalizeVikunjaBaseURL(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("endpoint identity %q: got %q, want %q; err=%v", tc.input, got, tc.want, err)
		}
	}
}

func TestVikunjaEndpointRejectsAmbiguousDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"vikunja.example",
		"ftp://vikunja.example",
		"https://user:password@vikunja.example",
		"https://vikunja.example/private?prefix=",
		"https://vikunja.example/#fragment",
		"https://vikunja.example:65536",
		"https://vikunja.example:",
		"https://vikunja.example/a/%2e%2e/b",
		"https://vikunja.example/a%2fb",
	} {
		if _, err := NormalizeVikunjaBaseURL(endpoint); err == nil {
			t.Errorf("accepted invalid configured endpoint %q", endpoint)
		}
	}
}
