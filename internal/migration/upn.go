package migration

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrExternalIdentity = errors.New("external email identities are not migrated in phase 1")
	ErrUnknownIdentity  = errors.New("cn is not a supported phase 1 identity")
)

func BuildUPN(cn string, primaryDomain string) (string, error) {
	normalizedCN := strings.TrimSpace(cn)
	domain := strings.ToLower(strings.TrimSpace(primaryDomain))
	if normalizedCN == "" {
		return "", ErrUnknownIdentity
	}
	if domain == "" {
		return "", errors.New("entra primary domain is required")
	}

	switch ClassifyCN(normalizedCN) {
	case IdentityStudentID, IdentityEmployeeID:
		return fmt.Sprintf("%s@%s", normalizedCN, domain), nil
	case IdentityExternalEmail:
		return "", ErrExternalIdentity
	default:
		return "", ErrUnknownIdentity
	}
}
