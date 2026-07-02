package graphclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

const (
	defaultBaseURL = "https://graph.microsoft.com"
	defaultScope   = "https://graph.microsoft.com/.default"
)

type User struct {
	UPN         string
	DisplayName string
	Mail        string
}

type Client interface {
	UpsertUserPassword(context.Context, User, []byte) error
}

type TokenCredential interface {
	GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)
}

type Options struct {
	BaseURL               string
	HTTPClient            *http.Client
	Scope                 string
	AfterRequestBodyBuilt func([]byte)
}

type HTTPClient struct {
	baseURL               *url.URL
	httpClient            *http.Client
	credential            TokenCredential
	scope                 string
	afterRequestBodyBuilt func([]byte)
}

type PermanentError struct {
	StatusCode int
	Operation  string
	Err        error
}

func (e *PermanentError) Error() string {
	return graphErrorString("permanent graph error", e.StatusCode, e.Operation, e.Err)
}

func (e *PermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type TransientError struct {
	StatusCode int
	Operation  string
	Err        error
}

func (e *TransientError) Error() string {
	return graphErrorString("transient graph error", e.StatusCode, e.Operation, e.Err)
}

func (e *TransientError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewHTTPClient(credential TokenCredential, options Options) (*HTTPClient, error) {
	if credential == nil {
		return nil, errors.New("graph token credential is required")
	}
	rawBaseURL := strings.TrimSpace(options.BaseURL)
	if rawBaseURL == "" {
		rawBaseURL = defaultBaseURL
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		if err == nil {
			err = errors.New("missing scheme or host")
		}
		return nil, fmt.Errorf("graph base URL is invalid: %w", err)
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	scope := strings.TrimSpace(options.Scope)
	if scope == "" {
		scope = defaultScope
	}
	return &HTTPClient{
		baseURL:               baseURL,
		httpClient:            httpClient,
		credential:            credential,
		scope:                 scope,
		afterRequestBodyBuilt: options.AfterRequestBodyBuilt,
	}, nil
}

func (c *HTTPClient) UpsertUserPassword(ctx context.Context, user User, password []byte) error {
	upn := strings.TrimSpace(user.UPN)
	if upn == "" {
		return &PermanentError{Operation: "validate user", Err: errors.New("user UPN is required")}
	}
	if len(password) == 0 {
		return &PermanentError{Operation: "validate password", Err: errors.New("password is required")}
	}

	status, err := c.doGraphRequest(ctx, "lookup user", http.MethodGet, c.userPath(upn), nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return c.patchUserPassword(ctx, upn, password)
	case http.StatusNotFound:
		user.UPN = upn
		return c.createUser(ctx, user, password)
	default:
		return classifyGraphResponse("lookup user", status)
	}
}

func (c *HTTPClient) patchUserPassword(ctx context.Context, upn string, password []byte) error {
	body := buildPatchBody(password)
	if c.afterRequestBodyBuilt != nil {
		c.afterRequestBodyBuilt(body)
	}
	defer passwordcrypto.ZeroBytes(body)

	status, err := c.doGraphRequest(ctx, "patch user", http.MethodPatch, c.userPath(upn), body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return classifyGraphResponse("patch user", status)
	}
	return nil
}

func (c *HTTPClient) createUser(ctx context.Context, user User, password []byte) error {
	body := buildCreateBody(user, password)
	if c.afterRequestBodyBuilt != nil {
		c.afterRequestBodyBuilt(body)
	}
	defer passwordcrypto.ZeroBytes(body)

	status, err := c.doGraphRequest(ctx, "create user", http.MethodPost, "/v1.0/users", body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return classifyGraphResponse("create user", status)
	}
	return nil
}

func (c *HTTPClient) doGraphRequest(ctx context.Context, operation string, method string, path string, body []byte) (int, error) {
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{c.scope}})
	if err != nil {
		return 0, &TransientError{Operation: operation, Err: fmt.Errorf("get graph token: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.graphURL(path), bytes.NewReader(body))
	if err != nil {
		return 0, &TransientError{Operation: operation, Err: fmt.Errorf("build graph request: %w", err)}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, &TransientError{Operation: operation, Err: err}
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

func (c *HTTPClient) graphURL(path string) string {
	u := *c.baseURL
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/") + path
}

func (c *HTTPClient) userPath(upn string) string {
	return "/v1.0/users/" + graphPathSegment(upn)
}

func buildPatchBody(password []byte) []byte {
	body := []byte(`{"passwordProfile":{"password":`)
	body = appendJSONString(body, password)
	body = append(body, `,"forceChangePasswordNextSignIn":false}}`...)
	return body
}

func buildCreateBody(user User, password []byte) []byte {
	body := []byte(`{"accountEnabled":true,"displayName":`)
	body = appendJSONStringFromString(body, user.DisplayName)
	body = append(body, `,"mailNickname":`...)
	body = appendJSONStringFromString(body, mailNickname(user.UPN))
	body = append(body, `,"userPrincipalName":`...)
	body = appendJSONStringFromString(body, user.UPN)
	mail := strings.TrimSpace(user.Mail)
	if mail != "" {
		body = append(body, `,"mail":`...)
		body = appendJSONStringFromString(body, mail)
		body = append(body, `,"otherMails":[`...)
		body = appendJSONStringFromString(body, mail)
		body = append(body, ']')
	}
	body = append(body, `,"passwordProfile":{"password":`...)
	body = appendJSONString(body, password)
	body = append(body, `,"forceChangePasswordNextSignIn":false}}`...)
	return body
}

func appendJSONString(dst []byte, value []byte) []byte {
	dst = append(dst, '"')
	for _, b := range value {
		switch b {
		case '\\', '"':
			dst = append(dst, '\\', b)
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if b < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hexDigit(b>>4), hexDigit(b))
				continue
			}
			dst = append(dst, b)
		}
	}
	dst = append(dst, '"')
	return dst
}

func appendJSONStringFromString(dst []byte, value string) []byte {
	return appendJSONString(dst, []byte(value))
}

func mailNickname(upn string) string {
	local, _, found := strings.Cut(upn, "@")
	if found && strings.TrimSpace(local) != "" {
		return local
	}
	return upn
}

func graphPathSegment(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), "@", "%40")
}

func classifyGraphResponse(operation string, status int) error {
	switch status {
	case http.StatusBadRequest, http.StatusForbidden:
		return &PermanentError{StatusCode: status, Operation: operation}
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return &TransientError{StatusCode: status, Operation: operation}
	default:
		return &TransientError{StatusCode: status, Operation: operation}
	}
}

func graphErrorString(prefix string, statusCode int, operation string, err error) string {
	var b strings.Builder
	b.WriteString(prefix)
	if operation != "" {
		b.WriteString(" during ")
		b.WriteString(operation)
	}
	if statusCode != 0 {
		b.WriteString(": status ")
		b.WriteString(fmt.Sprint(statusCode))
	}
	if err != nil {
		if statusCode != 0 {
			b.WriteString(": ")
		} else {
			b.WriteString(": ")
		}
		b.WriteString(err.Error())
	}
	return b.String()
}

func hexDigit(b byte) byte {
	return "0123456789abcdef"[b&0x0f]
}
