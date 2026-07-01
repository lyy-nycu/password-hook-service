# ADR 2026-07-01: Password Payload Encryption

## Status

Accepted

## Context

The password hook service receives cleartext passwords only at the moment of a successful portal login because Microsoft Entra ID cannot accept OpenLDAP password hashes. The original design relied on TLS, HMAC request signing, Service Bus encryption at rest, a short queue TTL, logging redaction, and DLQ filtering to reduce exposure.

That is not enough for the queue boundary. Service Bus encryption at rest protects the storage substrate, but any principal with receive/debug access to the queue can still read the message body. Native Service Bus dead-letter behavior can also preserve the original message body after terminal failures. If the queued message contains `password`, operational access to Service Bus becomes operational access to cleartext passwords.

## Decision

Password sync payloads are encrypted by the application before enqueue using an authenticated AES-256-GCM envelope. The Service Bus message body stores only:

- `cn`
- `upn`
- `passwordCiphertext`
- `passwordNonce`
- `passwordKeyId`
- `passwordAlg`
- non-secret identity metadata such as `displayName`, `mail`, and `enqueuedAt`

The hook service encrypts the password before enqueue. The worker decrypts the password only for the current Microsoft Graph attempt, passes it to the Graph processing path, and clears the plaintext buffer before retry backoff or message settlement.

Service Bus application properties must not contain the cleartext password, password ciphertext, nonce, or any other password-derived material.

Native Service Bus DLQ is not used for password sync payloads. Terminal failures are written to an application-level safe DLQ record containing only `cn`, `upn`, reason, attempts, and timestamps. After that safe DLQ write succeeds, the original encrypted password sync message is completed.

The password payload encryption key is stored in Azure Key Vault under `password-payload-encryption-key`. Service Bus encryption at rest remains enabled, but it is treated as storage-layer protection, not password-field protection.

## Alternatives Considered

### Keep Service Bus TTL plus encryption at rest

Rejected. This reduces retention but does not prevent Service Bus readers or native DLQ inspection from seeing cleartext message bodies.

### Do not queue password sync work

Rejected for Phase 1. Synchronous Graph calls would add latency and failure coupling to portal login, conflicting with the fire-and-forget requirement.

### Store only a password hash in the queue

Rejected. Entra ID password create/update APIs require cleartext password material at the time of the Graph call.

### Use Key Vault encrypt/decrypt per message

Deferred. It would centralize key operations but adds a remote dependency and latency to every enqueue/dequeue attempt. A local AES-GCM codec using a Key Vault-loaded secret keeps the queue boundary encrypted without making Key Vault part of the hot path.

## Consequences

- Service Bus message bodies and native broker storage no longer contain cleartext passwords.
- Service Bus operators without Key Vault access cannot recover queued passwords.
- Key Vault readers without Service Bus receive access cannot recover queued passwords.
- The worker must have access to the password payload encryption key.
- Key rotation must be additive first: deploy support for a new key id before retiring old keys that may still protect unexpired messages.
- Tests must enforce that queued JSON and Service Bus application properties do not contain plaintext password fields.
- Operational DLQ review uses safe failure records only; replay of failed password sync work requires the user to log in again.

## References

- `docs/superpowers/plans/superseded/2026-06-30-password-payload-encryption.md`
- `docs/superpowers/specs/2026-06-24-password-hook-service-design.md`
