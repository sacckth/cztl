package main

import (
	"reflect"
	"testing"
)

func TestKeyValueMap(t *testing.T) {
	got, err := keyValueMap([]string{"type=none", "options=bind", "mountpoint=/proc"})
	if err != nil {
		t.Fatalf("keyValueMap(): %v", err)
	}
	want := map[string]string{"type": "none", "options": "bind", "mountpoint": "/proc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyValueMap() = %v, want %v", got, want)
	}
}

func TestKeyValueMapRejectsInvalidValue(t *testing.T) {
	if _, err := keyValueMap([]string{"invalid"}); err == nil {
		t.Fatal("keyValueMap() accepted a value without '='")
	}
}

func TestStringList(t *testing.T) {
	var values stringList
	for _, value := range []string{"one", "two"} {
		if err := values.Set(value); err != nil {
			t.Fatalf("Set(%q): %v", value, err)
		}
	}
	if got, want := values.String(), "one,two"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
