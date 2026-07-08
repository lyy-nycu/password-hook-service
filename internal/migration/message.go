package migration

import "time"

type PasswordSyncMessage struct {
	CN                 string    `json:"cn"`
	UPN                string    `json:"upn"`
	EventType          EventType `json:"eventType"`
	TraceID            string    `json:"traceId,omitempty"`
	Password           string    `json:"-"`
	PasswordCiphertext string    `json:"passwordCiphertext"`
	PasswordNonce      string    `json:"passwordNonce"`
	PasswordKeyID      string    `json:"passwordKeyId"`
	PasswordAlg        string    `json:"passwordAlg"`
	DisplayName        string    `json:"displayName"`
	Mail               string    `json:"mail"`
	EnqueuedAt         time.Time `json:"enqueuedAt"`
}
