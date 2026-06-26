package secretloader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/nycu/password-hook-service/internal/config"
)

type Getter interface {
	GetSecret(context.Context, string) (string, error)
}

type keyVaultClient interface {
	GetSecret(context.Context, string, string, *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error)
}

type keyVaultGetter struct {
	client keyVaultClient
}

func NewKeyVaultGetter(vaultURL string) (Getter, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	client, err := azsecrets.NewClient(vaultURL, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Key Vault secrets client: %w", err)
	}
	return keyVaultGetter{client: client}, nil
}

func (g keyVaultGetter) GetSecret(ctx context.Context, name string) (string, error) {
	resp, err := g.client.GetSecret(ctx, name, "", nil)
	if err != nil {
		return "", err
	}
	if resp.Value == nil {
		return "", fmt.Errorf("Key Vault secret %s has nil value", name)
	}
	return *resp.Value, nil
}

func Resolve(ctx context.Context, cfg config.Config, getter Getter) (config.Config, error) {
	if err := cfg.ValidateSecretLoadingInputs(); err != nil {
		return config.Config{}, err
	}

	switch cfg.SecretsSource {
	case config.SecretsSourceEnv:
		return cfg, nil
	case config.SecretsSourceKeyVault:
		if getter == nil {
			var err error
			getter, err = NewKeyVaultGetter(cfg.KeyVaultURL)
			if err != nil {
				return config.Config{}, err
			}
		}
		return resolveKeyVault(ctx, cfg, getter)
	default:
		return config.Config{}, errors.New("SECRETS_SOURCE must be env or keyvault")
	}
}

func resolveKeyVault(ctx context.Context, cfg config.Config, getter Getter) (config.Config, error) {
	hmacSecret, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.HMACSecret)
	if err != nil {
		return config.Config{}, err
	}
	serviceBusConnectionString, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.ServiceBusConnectionString)
	if err != nil {
		return config.Config{}, err
	}
	graphClientSecret, err := getRequiredSecret(ctx, getter, cfg.KeyVaultSecretNames.GraphClientSecret)
	if err != nil {
		return config.Config{}, err
	}

	cfg.HMACSecret = hmacSecret
	cfg.ServiceBusConnectionString = serviceBusConnectionString
	cfg.GraphClientSecret = graphClientSecret
	return cfg, nil
}

func getRequiredSecret(ctx context.Context, getter Getter, name string) (string, error) {
	value, err := getter.GetSecret(ctx, name)
	if err != nil {
		return "", fmt.Errorf("load Key Vault secret %s: %w", name, sanitizeSecretError(err))
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("load Key Vault secret %s: secret value is empty", name)
	}
	return value, nil
}

func sanitizeSecretError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("secret read failed")
}
