package migration

import (
	"strings"
	"unicode"
)

type IdentityType string

const (
	IdentityUnknown       IdentityType = "unknown"
	IdentityStudentID     IdentityType = "student_id"
	IdentityEmployeeID    IdentityType = "employee_id"
	IdentityExternalEmail IdentityType = "external_email"
)

func ClassifyCN(cn string) IdentityType {
	normalized := strings.TrimSpace(cn)
	if normalized == "" {
		return IdentityUnknown
	}
	if strings.Contains(normalized, "@") {
		return IdentityExternalEmail
	}
	if allDigits(normalized) {
		return IdentityStudentID
	}
	if startsWithLetter(normalized) && allLettersDigitsOrHyphen(normalized) {
		return IdentityEmployeeID
	}
	return IdentityUnknown
}

func allDigits(value string) bool {
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func startsWithLetter(value string) bool {
	for _, r := range value {
		return unicode.IsLetter(r)
	}
	return false
}

func allLettersDigitsOrHyphen(value string) bool {
	for _, r := range value {
		if r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}
