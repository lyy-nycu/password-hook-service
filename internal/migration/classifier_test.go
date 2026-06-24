package migration

import "testing"

func TestClassifyCN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cn   string
		want IdentityType
	}{
		{name: "student id", cn: "311551001", want: IdentityStudentID},
		{name: "employee id", cn: "A12345", want: IdentityEmployeeID},
		{name: "external email", cn: "abc@gmail.com", want: IdentityExternalEmail},
		{name: "trimmed student id", cn: " 311551001 ", want: IdentityStudentID},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ClassifyCN(tt.cn)
			if got != tt.want {
				t.Fatalf("ClassifyCN(%q) = %q, want %q", tt.cn, got, tt.want)
			}
		})
	}
}
