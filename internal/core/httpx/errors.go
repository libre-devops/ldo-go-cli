package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// FromResponse is an API error for a failed response, from the service's error body when
// it has one.
//
// hints gives the hint for an error code the API is known to give, or for a status
// ("HTTP 401") when its errors carry no code, before the general ones.
func FromResponse(name string, response *http.Response, body []byte, hints map[string]string) *errs.Error {
	var decoded any
	_ = json.Unmarshal(body, &decoded)
	code, message, requestID := errorFields(decoded)
	if requestID == "" {
		requestID = response.Header.Get("request-id")
	}
	if requestID == "" {
		requestID = response.Header.Get("x-ms-request-id")
	}
	status := response.StatusCode
	detail := ""
	if message != "" {
		detail = Readable(message)
	}
	if detail == "" {
		detail = http.StatusText(status)
	}
	if detail == "" {
		detail = "request failed"
	}
	text := fmt.Sprintf("%s: HTTP %d", name, status)
	if code != "" {
		text += " " + code
	}
	text += ": " + detail
	if requestID != "" {
		text += " (request-id " + requestID + ")"
	}
	err := errs.APIf("%s", text)
	err.Status, err.Code, err.RequestID = status, code, requestID
	challenge := strings.ToLower(response.Header.Get("WWW-Authenticate"))
	if status == http.StatusUnauthorized && strings.Contains(challenge, "insufficient_claims") {
		// Continuous access evaluation: the token was revoked, or a policy changed.
		err.Hint = "the service revoked the token or wants a sign-in that meets a Conditional " +
			"Access policy (a claims challenge); sign in again"
		return err
	}
	if code != "" {
		err.Hint = hints[code]
	}
	if err.Hint == "" {
		err.Hint = hints[fmt.Sprintf("HTTP %d", status)]
	}
	if err.Hint == "" {
		err.Hint = generalHint(status, strings.ToLower(text))
	}
	return err
}

// errorFields is the (code, message, request id) of an error body, in the shapes
// services send: Graph, Defender, ARM and Key Vault send {"error": {"code", "message"}};
// the Entra token endpoint {"error": "<code>", "error_description": "..."}; Jira
// {"errorMessages": [...]}, and Confluence {"errors": [{"title", "detail"}]}.
func errorFields(body any) (code, message, requestID string) {
	object, ok := body.(map[string]any)
	if !ok {
		return "", "", ""
	}
	switch value := object["error"].(type) {
	case map[string]any:
		code, _ = value["code"].(string)
		message, _ = value["message"].(string)
		if inner, isObject := value["innerError"].(map[string]any); isObject {
			requestID, _ = inner["request-id"].(string)
		}
		return code, message, requestID
	case string:
		description, _ := object["error_description"].(string)
		// The first line carries the AADSTS code and the reason; the rest is trace ids.
		first, _, _ := strings.Cut(strings.TrimSpace(description), "\n")
		return value, strings.TrimSpace(first), ""
	}
	return "", atlassianMessage(object), ""
}

func atlassianMessage(body map[string]any) string {
	if messages, ok := body["errorMessages"].([]any); ok && len(messages) > 0 {
		var parts []string
		for _, message := range messages {
			parts = append(parts, fmt.Sprint(message))
		}
		return strings.Join(parts, "; ")
	}
	if errors, ok := body["errors"].([]any); ok && len(errors) > 0 {
		if first, isObject := errors[0].(map[string]any); isObject {
			var parts []string
			for _, key := range []string{"title", "detail"} {
				if text, isText := first[key].(string); isText && text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, ": ")
		}
	}
	return ""
}

// Readable is one readable line from a service error message.
//
// Some services put a JSON document inside the message (Graph's PIM errors, Intune, which
// nests two deep); those are unwrapped to their inner code and message. Line breaks are
// flattened, and the result is capped so one error cannot flood a terminal.
func Readable(message string) string {
	var codes []string
	text := strings.TrimSpace(message)
	for range 3 {
		if !strings.HasPrefix(text, "{") {
			break
		}
		var inner map[string]any
		if json.Unmarshal([]byte(text), &inner) != nil {
			break
		}
		for _, key := range []string{"errorCode", "ErrorCode", "code"} {
			if code, ok := inner[key].(string); ok && code != "" {
				codes = append(codes, code)
				break
			}
		}
		found, ok := inner["message"].(string)
		if !ok {
			found, ok = inner["Message"].(string)
		}
		if !ok {
			text = ""
			break
		}
		text = strings.TrimSpace(found)
	}
	flat := strings.Join(strings.Fields(text), " ")
	parts := codes
	if flat != "" {
		parts = append(parts, flat)
	}
	readable := strings.Join(parts, ": ")
	if len(readable) > 400 {
		return readable[:397] + "..."
	}
	return readable
}

func generalHint(status int, text string) string {
	switch {
	case status == 403 && strings.Contains(text, "suspended"):
		return "the service is suspended in this tenant, usually because its licence or trial ended"
	case status == 403 && strings.Contains(text, "client address is not authorized"):
		return "the resource's firewall does not allow this machine's IP address"
	case (status == 401 || status == 403) && (strings.Contains(text, "forbidden") || strings.Contains(text, "permission")):
		return "the token was accepted but lacks the permission this call needs"
	case status == 401:
		return "the API rejected the token; check its audience, tenant and expiry"
	case status == 403:
		return "the signed-in identity lacks a role or permission for this call"
	}
	return ""
}
