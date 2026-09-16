//go:build !windows && !linux && !darwin

package updateproxy

import (
	"net/http"
	"net/url"
)

func systemProxy(*http.Request) (*url.URL, error) { return nil, nil }
