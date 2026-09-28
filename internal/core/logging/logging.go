// Package logging sets up the CLI's logs: human text, flat JSON, or the OpenTelemetry
// wire format, on stderr so they never mix with data on stdout. Library packages only
// call slog; the CLI calls Configure once.
//
// otlp writes the OpenTelemetry file format: JSON Lines, each line a complete OTLP/JSON
// LogsData holding one record. An OpenTelemetry Collector reads it with the
// otlp_json_file receiver, or from a container's logs with the file_log receiver and the
// otlp_json connector. It reads the log settings from LDO_LOG_FORMAT, LDO_LOG_LEVEL,
// LDO_SERVICE_NAME and the trace context, as the Python ldo does.
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
)

// Formats are the log line formats.
var Formats = []string{"text", "json", "otlp"}

// Other names formats and levels go by, which mean the same.
var formatAliases = map[string]string{"jsonindented": "json", "otlpindented": "otlp"}

var levels = map[string]slog.Level{
	"trace": slog.LevelDebug, "debug": slog.LevelDebug, "info": slog.LevelInfo, "success": slog.LevelInfo,
	"warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "fatal": slog.LevelError + 4,
}

// NormaliseFormat is text, json or otlp for any spelling of one.
func NormaliseFormat(format string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(format))
	if value == "" {
		return "text", nil
	}
	if alias, ok := formatAliases[value]; ok {
		value = alias
	}
	for _, known := range Formats {
		if value == known {
			return value, nil
		}
	}
	return "", fmt.Errorf("%q is not a log format; use text, json or otlp", format)
}

// Configure makes the logger every record goes to: -v info, -vv debug, else level (any of
// trace, debug, info, warn, error, fatal), else warnings and worse. It is the default
// logger from then on. structured is true for json and otlp, where the CLI's own notes
// and warnings become records too, so stderr is clean JSON Lines.
func Configure(w io.Writer, verbose int, format, level string) (*slog.Logger, bool, error) {
	name, err := NormaliseFormat(format)
	if err != nil {
		return nil, false, err
	}
	minimum := slog.LevelWarn
	if found, ok := levels[strings.ToLower(strings.TrimSpace(level))]; ok {
		minimum = found
	}
	switch {
	case verbose >= 2:
		minimum = slog.LevelDebug
	case verbose == 1:
		minimum = slog.LevelInfo
	}
	var handler slog.Handler
	switch name {
	case "json":
		handler = &jsonHandler{w: w, level: minimum}
	case "otlp":
		handler = newOTLPHandler(w, minimum, os.Getenv)
	default:
		file, _ := w.(*os.File)
		handler = &briefHandler{w: w, level: minimum, coloured: colour.Wanted(file)}
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger, name != "text", nil
}

var mu sync.Mutex

func write(w io.Writer, line string) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(w, line)
}

func attributes(record slog.Record, group []slog.Attr) map[string]any {
	found := map[string]any{}
	for _, attr := range group {
		found[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		found[attr.Key] = attr.Value.Any()
		return true
	})
	return found
}

func levelName(level slog.Level) string {
	switch {
	case level >= slog.LevelError+4:
		return "fatal"
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warning"
	case level >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

// briefHandler writes a record as the command's own messages read, "warning: ...".
type briefHandler struct {
	w        io.Writer
	level    slog.Level
	coloured bool
	attrs    []slog.Attr
}

func (h *briefHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *briefHandler) Handle(_ context.Context, record slog.Record) error {
	text := levelName(record.Level) + ": " + record.Message
	for key, value := range attributes(record, h.attrs) {
		if key != "hint" {
			text += fmt.Sprintf(" %s=%v", key, value)
		}
	}
	if h.coloured {
		switch {
		case record.Level >= slog.LevelError:
			text = colour.Style(text, "red", false, false)
		case record.Level >= slog.LevelWarn:
			text = colour.Style(text, "yellow", false, false)
		}
	}
	write(h.w, text)
	return nil
}

func (h *briefHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copied := *h
	copied.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &copied
}

func (h *briefHandler) WithGroup(string) slog.Handler { return h }

// jsonHandler writes one flat JSON object per record.
type jsonHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
}

func (h *jsonHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *jsonHandler) Handle(_ context.Context, record slog.Record) error {
	data := map[string]any{
		"time":    record.Time.Format("2006-01-02T15:04:05.000"),
		"level":   levelName(record.Level),
		"logger":  brand.Command,
		"message": record.Message,
	}
	for key, value := range attributes(record, h.attrs) {
		data[key] = value
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	write(h.w, string(encoded))
	return nil
}

func (h *jsonHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copied := *h
	copied.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &copied
}

func (h *jsonHandler) WithGroup(string) slog.Handler { return h }

// https://opentelemetry.io/docs/specs/otel/logs/data-model/#field-severitynumber
func severity(level slog.Level) int {
	switch {
	case level >= slog.LevelError+4:
		return 21
	case level >= slog.LevelError:
		return 17
	case level >= slog.LevelWarn:
		return 13
	case level >= slog.LevelInfo:
		return 9
	}
	return 5
}

// otlpHandler writes one OTLP/JSON LogsData per record, on one line.
//
// OTLP's JSON rules differ from plain proto3 JSON: 64-bit integers such as timestamps are
// strings, severityNumber is the integer, traceId and spanId are lowercase hex, and
// attribute values are typed. The resource is service.name (LDO_SERVICE_NAME, else
// OTEL_SERVICE_NAME, else the command), service.version and, with
// LDO_DEPLOYMENT_ENVIRONMENT, deployment.environment.name; anything else comes from
// OTEL_RESOURCE_ATTRIBUTES. LDO_TRACE_ID, LDO_SPAN_ID and LDO_CORRELATION_ID put every
// record in a trace; an id that is not valid hex of the right width is left out.
type otlpHandler struct {
	w           io.Writer
	level       slog.Level
	resource    map[string]any
	traceID     string
	spanID      string
	correlation string
	attrs       []slog.Attr
}

func newOTLPHandler(w io.Writer, level slog.Level, getenv func(string) string) *otlpHandler {
	correlation := strings.TrimSpace(getenv(brand.EnvVar("CORRELATION_ID")))
	traceID := HexID(getenv(brand.EnvVar("TRACE_ID")), 32)
	if traceID == "" {
		traceID = HexID(correlation, 32)
	}
	return &otlpHandler{
		w: w, level: level, resource: map[string]any{"attributes": resourceAttributes(getenv)},
		traceID: traceID, spanID: HexID(getenv(brand.EnvVar("SPAN_ID")), 16), correlation: correlation,
	}
}

// HexID is a trace (32) or span (16) id in OTLP's lowercase hex, or "" when value is not
// one. Dashes are dropped, so a GUID becomes a trace id, and an all-zero id is refused.
func HexID(value string, length int) string {
	candidate := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if len(candidate) != length || strings.Trim(candidate, "0123456789abcdef") != "" || strings.Trim(candidate, "0") == "" {
		return ""
	}
	return candidate
}

func resourceAttributes(getenv func(string) string) []map[string]any {
	extra := map[string]string{}
	var order []string
	for _, pair := range strings.Split(getenv("OTEL_RESOURCE_ATTRIBUTES"), ",") {
		key, value, found := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); found && key != "" {
			decodedKey, _ := url.QueryUnescape(key)
			decodedValue, _ := url.QueryUnescape(strings.TrimSpace(value))
			if _, seen := extra[decodedKey]; !seen {
				order = append(order, decodedKey)
			}
			extra[decodedKey] = decodedValue
		}
	}
	first := func(values ...string) string {
		for _, value := range values {
			if value != "" {
				return value
			}
		}
		return ""
	}
	name := first(getenv(brand.EnvVar("SERVICE_NAME")), getenv("OTEL_SERVICE_NAME"), extra["service.name"], brand.Command)
	version := first(getenv(brand.EnvVar("SERVICE_VERSION")), extra["service.version"], brand.Version)
	environment := first(getenv(brand.EnvVar("DEPLOYMENT_ENVIRONMENT")), extra["deployment.environment.name"])
	found := []map[string]any{attribute("service.name", name), attribute("service.version", version)}
	if environment != "" {
		found = append(found, attribute("deployment.environment.name", environment))
	}
	for _, key := range order {
		if key != "service.name" && key != "service.version" && key != "deployment.environment.name" {
			found = append(found, attribute(key, extra[key]))
		}
	}
	return found
}

func attribute(key string, value any) map[string]any {
	switch v := value.(type) {
	case int:
		return map[string]any{"key": key, "value": map[string]any{"intValue": fmt.Sprint(v)}}
	case int64:
		return map[string]any{"key": key, "value": map[string]any{"intValue": fmt.Sprint(v)}}
	case bool:
		return map[string]any{"key": key, "value": map[string]any{"boolValue": v}}
	}
	return map[string]any{"key": key, "value": map[string]any{"stringValue": fmt.Sprint(value)}}
}

func (h *otlpHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *otlpHandler) Handle(_ context.Context, record slog.Record) error {
	when := record.Time
	if when.IsZero() {
		when = time.Now()
	}
	nanos := fmt.Sprint(when.UnixNano())
	var attrs []map[string]any
	if h.correlation != "" {
		attrs = append(attrs, attribute("correlation_id", h.correlation))
	}
	for key, value := range attributes(record, h.attrs) {
		attrs = append(attrs, attribute(key, value))
	}
	logRecord := map[string]any{
		"timeUnixNano": nanos, "observedTimeUnixNano": nanos,
		"severityNumber": severity(record.Level), "severityText": strings.ToUpper(levelName(record.Level)),
		"body": map[string]any{"stringValue": record.Message}, "attributes": attrs,
	}
	if h.traceID != "" {
		logRecord["traceId"] = h.traceID
	}
	if h.spanID != "" {
		logRecord["spanId"] = h.spanID
	}
	request := map[string]any{"resourceLogs": []any{map[string]any{
		"resource":  h.resource,
		"scopeLogs": []any{map[string]any{"scope": map[string]any{"name": brand.Command}, "logRecords": []any{logRecord}}},
	}}}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	write(h.w, string(encoded))
	return nil
}

func (h *otlpHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copied := *h
	copied.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &copied
}

func (h *otlpHandler) WithGroup(string) slog.Handler { return h }
