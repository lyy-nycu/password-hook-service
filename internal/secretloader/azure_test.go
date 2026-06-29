package secretloader

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

func TestKeyVaultGetterRejectsNilSecretValue(t *testing.T) {
	t.Parallel()

	getter := keyVaultGetter{client: fakeKeyVaultClient{}}

	_, err := getter.GetSecret(context.Background(), "hook-hmac-secret")
	if err == nil || err.Error() != "Key Vault secret hook-hmac-secret has nil value" {
		t.Fatalf("GetSecret error = %v, want nil value error", err)
	}
}

type fakeKeyVaultClient struct{}

func (fakeKeyVaultClient) GetSecret(context.Context, string, string, *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	return azsecrets.GetSecretResponse{}, nil
}
