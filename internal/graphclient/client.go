package graphclient

import "context"

type User struct {
	UPN         string
	DisplayName string
	Mail        string
}

type Client interface {
	UpsertUserPassword(context.Context, User, string) error
}
