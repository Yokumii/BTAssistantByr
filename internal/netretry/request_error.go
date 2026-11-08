package netretry

import (
	"errors"
	"net/url"
)

// RequestError keeps transport error identity while omitting URL credentials and
// query parameters from messages that may be written to logs.
func RequestError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &requestError{operation: operation, cause: err}
}

type requestError struct {
	operation string
	cause     error
}

func (err *requestError) Error() string {
	return err.operation + ": " + requestErrorDetail(err.cause)
}

func (err *requestError) Unwrap() error {
	return err.cause
}

func requestErrorDetail(err error) string {
	if err == nil {
		return "unknown transport failure"
	}
	var urlError *url.Error
	if !errors.As(err, &urlError) {
		return err.Error()
	}
	endpoint := "[invalid URL]"
	if parsed, parseErr := url.Parse(urlError.URL); parseErr == nil && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		endpoint = parsed.String()
	}
	return urlError.Op + " " + endpoint + ": " + requestErrorDetail(urlError.Err)
}
