package mapping

import "testing"

func TestNewUUIDv7UsesCanonicalVersionSevenEncoding(t *testing.T) {
	first, err := NewUUIDv7()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUUIDv7()
	if err != nil {
		t.Fatal(err)
	}
	if !IsUUIDv7(first) || !IsUUIDv7(second) {
		t.Fatalf("generated IDs are not canonical UUIDv7 values: %q, %q", first, second)
	}
	if first == second {
		t.Fatal("UUIDv7 generator repeated an ID")
	}
	if IsUUIDv7("018cc251-f400-6c8b-a991-d09a0f20018d") || IsUUIDv7("018CC251-f400-7c8b-a991-d09a0f20018d") {
		t.Fatal("UUIDv7 validator accepted a non-canonical UUID")
	}
}

func TestBuiltInProfileIDIsStableUUIDv7(t *testing.T) {
	first := BuiltInProfileID("builtin-active-low")
	if !IsUUIDv7(first) {
		t.Fatalf("built-in ID = %q", first)
	}
	if again := BuiltInProfileID("builtin-active-low"); again != first {
		t.Fatalf("built-in ID changed: %q -> %q", first, again)
	}
	if other := BuiltInProfileID("builtin-ratio-percent"); other == first {
		t.Fatal("distinct built-in identifiers share one UUID")
	}
}
