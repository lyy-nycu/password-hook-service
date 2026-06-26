package secretloader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nycu/password-hook-service/internal/config"
)

type Getter interface {
	GetSecret(context.Context, string) (string, error)
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
			return config.Config{}, errors.New("key vault getter is required")
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
