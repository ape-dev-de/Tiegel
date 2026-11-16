package version

import "testing"

func TestDefaults(t *testing.T) {
	if !IsDev() {
		t.Fatalf("IsDev() = false for default version %q", Version)
	}
	want := "Tiegel dev (commit unknown, built unknown)"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestInjected(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })

	Version, Commit, Date = "v1.2.3", "abc1234", "2026-01-02T03:04:05Z"
	if IsDev() {
		t.Fatal("IsDev() = true for injected version")
	}
	want := "Tiegel v1.2.3 (commit abc1234, built 2026-01-02T03:04:05Z)"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	Version = ""
	if !IsDev() {
		t.Fatal("IsDev() = false for empty version")
	}
}
