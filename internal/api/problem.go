package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/i18n"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type problem struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail"`
	Code   string              `json:"code"`
	Params map[string]any      `json:"params,omitempty"`
	Errors []problemFieldError `json:"errors,omitempty"`
}

type problemFieldError struct {
	Code    string         `json:"code"`
	Pointer string         `json:"pointer"`
	Params  map[string]any `json:"params,omitempty"`
}

type responseError struct {
	status  int
	code    string
	pointer string
	params  map[string]any
}

// UnavailableHandler also serves failures detected before the API router runs.
func (s *Server) UnavailableHandler() http.Handler {
	return s.localeMiddleware(s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeProblem(w, r, http.StatusServiceUnavailable, "system.not_ready", nil, "")
	})))
}

func (e responseError) Error() string { return e.code }

func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, status int, code string, params map[string]any, pointer string) {
	document := s.problemDocument(r, status, code, params, pointer)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(document.Status)
	_ = json.NewEncoder(w).Encode(document)
}

func (s *Server) problemDocument(r *http.Request, status int, code string, params map[string]any, pointer string) problem {
	locale := i18n.FromContext(r.Context())
	detailKey := "problem." + code
	if !i18n.Has(locale, detailKey) {
		code = "internal.operation_failed"
		detailKey = "problem.internal.operation_failed"
		params = nil
		pointer = ""
		status = http.StatusInternalServerError
	}
	document := problem{
		Type:   "urn:nsc:problem:" + code,
		Title:  i18n.Text(locale, problemTitleKey(status, code), nil),
		Status: status,
		Detail: i18n.Text(locale, detailKey, params),
		Code:   code,
		Params: params,
	}
	if pointer != "" {
		document.Errors = []problemFieldError{{Code: code, Pointer: jsonPointer(pointer), Params: params}}
	}
	return document
}

func problemTitleKey(status int, code string) string {
	switch {
	case strings.HasPrefix(code, "validation."):
		return "problem.title.validation"
	case code == "system.not_ready":
		return "problem.title.not_ready"
	case code == "rate_limit.exceeded":
		return "problem.title.rate_limit"
	case strings.HasPrefix(code, "auth."):
		return "problem.title.unauthorized"
	case strings.HasPrefix(code, "csrf."):
		return "problem.title.forbidden"
	case strings.HasPrefix(code, "request."):
		return "problem.title.bad_request"
	case strings.HasPrefix(code, "resource."):
		return "problem.title.not_found"
	case strings.HasPrefix(code, "conflict."):
		return "problem.title.conflict"
	case strings.HasPrefix(code, "precondition."):
		return "problem.title.precondition"
	case strings.HasPrefix(code, "storage."):
		return "problem.title.storage"
	case status == http.StatusBadRequest:
		return "problem.title.bad_request"
	default:
		return "problem.title.internal"
	}
}

func jsonPointer(field string) string {
	parts := strings.Split(field, ".")
	for index, part := range parts {
		part = strings.ReplaceAll(part, "~", "~0")
		parts[index] = strings.ReplaceAll(part, "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, target any, max int64) bool {
	if max <= 0 {
		max = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.body_too_large", nil, "")
			return false
		}
		s.logger.DebugContext(r.Context(), "decode request JSON", "error", err)
		s.writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", nil, "")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeProblem(w, r, http.StatusBadRequest, "request.multiple_json_objects", nil, "")
		return false
	}
	return true
}

func parseIfMatch(r *http.Request) (int64, error) {
	value := strings.Trim(r.Header.Get("If-Match"), `"`)
	value = strings.TrimPrefix(value, "rev-")
	if value == "" {
		return 0, responseError{status: http.StatusPreconditionRequired, code: "precondition.if_match_required"}
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 {
		return 0, responseError{status: http.StatusPreconditionRequired, code: "precondition.if_match_invalid"}
	}
	return revision, nil
}

func (s *Server) handleError(w http.ResponseWriter, r *http.Request, err error) {
	explicit, internal := classifyError(err)
	if internal {
		s.logger.ErrorContext(r.Context(), "request operation failed", "error", err)
	}
	s.writeProblem(w, r, explicit.status, explicit.code, explicit.params, explicit.pointer)
}

func classifyError(err error) (responseError, bool) {
	var explicit responseError
	var validation core.ValidationError
	switch {
	case errors.Is(err, storage.ErrSystemProtected):
		return responseError{status: http.StatusForbidden, code: "system.project_protected"}, false
	case errors.As(err, &explicit):
		return explicit, false
	case errors.As(err, &validation) && validation.Code == "auth.required":
		return responseError{status: http.StatusUnauthorized, code: validation.Code}, false
	case errors.As(err, &validation) && validation.Code == "management.control_disabled":
		return responseError{status: http.StatusForbidden, code: validation.Code}, false
	case errors.As(err, &validation) && validation.Code != "":
		return responseError{status: http.StatusBadRequest, code: validation.Code, params: validation.Params, pointer: validation.Field}, false
	case errors.Is(err, storage.ErrNotFound):
		return responseError{status: http.StatusNotFound, code: "resource.not_found"}, false
	case errors.Is(err, storage.ErrConflict):
		return responseError{status: http.StatusConflict, code: "conflict.resource"}, false
	case errors.Is(err, storage.ErrPrecondition):
		return responseError{status: http.StatusPreconditionFailed, code: "precondition.failed"}, false
	default:
		return responseError{status: http.StatusInternalServerError, code: "internal.operation_failed"}, true
	}
}
