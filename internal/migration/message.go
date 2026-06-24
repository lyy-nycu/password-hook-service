package migration

import "time"

type PasswordSyncMessage struct {
	CN          string    `json:"cn"`
	UPN         string    `json:"upn"`
	Password    string    `json:"password"`
	DisplayName string    `json:"displayName"`
	Mail        string    `json:"mail"`
	EnqueuedAt  time.Time `json:"enqueuedAt"`
}
