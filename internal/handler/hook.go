package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nycu/password-hook-service/internal/migration"
	"github.com/nycu/password-hook-service/internal/passwordcrypto"
	"github.com/nycu/password-hook-service/internal/requestid"
	"github.com/nycu/password-hook-service/pkg/problem"
)

type Hook struct {
	service        *migration.Service
	problemBaseURL string
}

func NewHook(service *migration.Service, problemBaseURL string) *Hook {
	return &Hook{
		service:        service,
		problemBaseURL: strings.TrimRight(problemBaseURL, "/"),
	}
}

func (h *Hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	defer passwordcrypto.ZeroBytes(rawBody)
	if err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be readable"))
		return
	}

	var body passwordHookRequest
	err = json.Unmarshal(rawBody, &body)
	defer passwordcrypto.ZeroBytes(body.Password)
	if err != nil {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "request body must be valid json"))
		return
	}
	if detail := body.validate(); detail != "" {
		h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), detail))
		return
	}

	_, err = h.service.Submit(r.Context(), migration.Request{
		CN:          body.CN,
		Password:    []byte(body.Password),
		DisplayName: body.DisplayName,
		Mail:        body.Mail,
	})
	if err != nil {
		if errors.Is(err, migration.ErrUnknownIdentity) || errors.Is(err, migration.ErrExternalIdentity) {
			h.writeProblem(w, r, problem.Validation(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), err.Error()))
			return
		}
		h.writeProblem(w, r, problem.Internal(h.problemBaseURL, r.URL.Path, requestid.From(r.Context()), "failed to accept password sync request"))
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Hook) writeProblem(w http.ResponseWriter, _ *http.Request, p problem.Problem) {
	problem.Write(w, p)
}

type passwordHookRequest struct {
	CN          string        `json:"cn"`
	Password    passwordBytes `json:"password"`
	DisplayName string        `json:"displayName"`
	Mail        string        `json:"mail"`
}

func (r passwordHookRequest) validate() string {
	switch {
	case strings.TrimSpace(r.CN) == "":
		return "Field 'cn' is required"
	case len(r.Password) == 0:
		return "Field 'password' is required"
	case strings.TrimSpace(r.DisplayName) == "":
		return "Field 'displayName' is required"
	case strings.TrimSpace(r.Mail) == "":
		return "Field 'mail' is required"
	default:
		return ""
	}
}

type passwordBytes []byte

func (p *passwordBytes) UnmarshalJSON(data []byte) error {
	passwordcrypto.ZeroBytes(*p)
	*p = nil

	if bytes.Equal(data, []byte("null")) {
		return nil
	}

	decoded, err := decodeJSONStringBytes(data)
	if err != nil {
		return err
	}
	*p = decoded
	return nil
}

func decodeJSONStringBytes(data []byte) (_ []byte, err error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return nil, errors.New("password must be a json string")
	}

	out := make([]byte, 0, len(data)-2)
	defer func() {
		if err != nil {
			passwordcrypto.ZeroBytes(out)
		}
	}()
	for i := 1; i < len(data)-1; i++ {
		b := data[i]
		if b != '\\' {
			if b < 0x20 {
				return nil, errors.New("password contains invalid json string control character")
			}
			if b < utf8.RuneSelf {
				out = append(out, b)
				continue
			}
			r, size := utf8.DecodeRune(data[i : len(data)-1])
			out = utf8.AppendRune(out, r)
			i += size - 1
			continue
		}

		i++
		if i >= len(data)-1 {
			return nil, errors.New("password contains invalid json escape")
		}
		switch data[i] {
		case '"', '\\', '/':
			out = append(out, data[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, consumed, err := decodeUnicodeEscape(data[i+1 : len(data)-1])
			if err != nil {
				return nil, err
			}
			i += consumed
			out = utf8.AppendRune(out, r)
		default:
			return nil, fmt.Errorf("password contains invalid json escape %q", data[i])
		}
	}
	return out, nil
}

func decodeUnicodeEscape(data []byte) (rune, int, error) {
	if len(data) < 4 {
		return 0, 0, errors.New("password contains short unicode escape")
	}
	r, err := hex4(data[:4])
	if err != nil {
		return 0, 0, err
	}
	if !utf16.IsSurrogate(r) {
		return r, 4, nil
	}
	if r < 0xD800 || r > 0xDBFF {
		return utf8.RuneError, 4, nil
	}
	if len(data) < 10 || data[4] != '\\' || data[5] != 'u' {
		return utf8.RuneError, 4, nil
	}
	low, err := hex4(data[6:10])
	if err != nil {
		return utf8.RuneError, 4, nil
	}
	decoded := utf16.DecodeRune(r, low)
	if decoded == utf8.RuneError {
		return utf8.RuneError, 4, nil
	}
	return decoded, 10, nil
}

func hex4(data []byte) (rune, error) {
	var r rune
	for _, b := range data {
		r <<= 4
		switch {
		case b >= '0' && b <= '9':
			r += rune(b - '0')
		case b >= 'a' && b <= 'f':
			r += rune(b-'a') + 10
		case b >= 'A' && b <= 'F':
			r += rune(b-'A') + 10
		default:
			return 0, fmt.Errorf("password contains invalid unicode escape byte %q", b)
		}
	}
	return r, nil
}
