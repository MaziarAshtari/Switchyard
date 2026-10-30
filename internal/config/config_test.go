package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"ENCRYPTION_KEY": "0123456789abcdef0123"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 || c.DataDir != "data" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	_, err := Load(env(map[string]string{"PORT": "abc", "ENCRYPTION_KEY": "short"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"PORT", "ENCRYPTION_KEY must be at least"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, err := Load(env(nil)); err == nil || !strings.Contains(err.Error(), "ENCRYPTION_KEY is required") {
		t.Errorf("missing key error = %v", err)
	}
}
