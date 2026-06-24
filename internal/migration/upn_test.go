package migration

import "testing"

func TestBuildUPN(t *testing.T) {
	t.Parallel()

	got, err := BuildUPN(" A12345 ", "nycu.edu.tw")
	if err != nil {
		t.Fatalf("BuildUPN returned error: %v", err)
	}

	want := "A12345@nycu.edu.tw"
	if got != want {
		t.Fatalf("BuildUPN() = %q, want %q", got, want)
	}
}

func TestBuildUPNRejectsExternalEmailCN(t *testing.T) {
	t.Parallel()

	_, err := BuildUPN("abc@gmail.com", "nycu.edu.tw")
	if err == nil {
		t.Fatal("BuildUPN returned nil error for external email cn")
	}
}
