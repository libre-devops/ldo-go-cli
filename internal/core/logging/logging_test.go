package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestBriefLinesReadAsTheCommandsOwn(t *testing.T) {
	var out bytes.Buffer
	logger, structured, err := Configure(&out, 0, "", "")
	if err != nil || structured {
		t.Fatal(err)
	}
	logger.Info("hidden")
	logger.Warn("careful", "count", 2)
	if out.String() != "warning: careful count=2\n" {
		t.Errorf("%q", out.String())
	}
}

func TestVerbosityAndLevels(t *testing.T) {
	var out bytes.Buffer
	logger, _, _ := Configure(&out, 2, "text", "error")
	logger.Debug("deep")
	if !strings.Contains(out.String(), "debug: deep") {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	logger, _, _ = Configure(&out, 0, "text", "error")
	logger.Warn("quiet")
	if out.Len() != 0 {
		t.Errorf("%q", out.String())
	}
}

func TestJSONLinesAreFlatRecords(t *testing.T) {
	var out bytes.Buffer
	logger, structured, _ := Configure(&out, 1, "jsonindented", "")
	logger.With("profile", "dev").Info("hello", "hint", "do this")
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil || !structured {
		t.Fatal(err)
	}
	if record["level"] != "info" || record["message"] != "hello" || record["profile"] != "dev" || record["hint"] != "do this" ||
		record["logger"] != "ldo-go" {
		t.Errorf("%v", record)
	}
}

func TestOTLPRecordsCarryTheResourceAndTrace(t *testing.T) {
	var out bytes.Buffer
	env := map[string]string{"LDO_TRACE_ID": "0AF7651916CD43DD8448EB211C80319C", "LDO_SPAN_ID": "b7ad6b7169203331",
		"LDO_CORRELATION_ID": "run-1", "LDO_DEPLOYMENT_ENVIRONMENT": "prod",
		"OTEL_RESOURCE_ATTRIBUTES": "team=sec%20ops,service.name=ignored"}
	logger := slog.New(newOTLPHandler(&out, slog.LevelInfo, func(name string) string { return env[name] }))
	logger.Error("failed", "count", 3, "ok", false)
	var data map[string]any
	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	resource := data["resourceLogs"].([]any)[0].(map[string]any)
	record := resource["scopeLogs"].([]any)[0].(map[string]any)["logRecords"].([]any)[0].(map[string]any)
	if record["severityNumber"] != float64(17) || record["traceId"] != "0af7651916cd43dd8448eb211c80319c" ||
		record["spanId"] != "b7ad6b7169203331" {
		t.Errorf("%v", record)
	}
	text := out.String()
	for _, want := range []string{`"service.name","value":{"stringValue":"ignored"}`, `"deployment.environment.name"`,
		`"team","value":{"stringValue":"sec ops"}`, `"correlation_id"`, `"intValue":"3"`, `"boolValue":false`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
}

func TestFormatsAndIDs(t *testing.T) {
	if _, err := NormaliseFormat("xml"); err == nil {
		t.Error("xml is not a format")
	}
	if format, _ := NormaliseFormat(" OTLPIndented "); format != "otlp" {
		t.Error(format)
	}
	if _, _, err := Configure(&bytes.Buffer{}, 0, "xml", ""); err == nil {
		t.Error("configured xml")
	}
	if HexID("00000000000000000000000000000000", 32) != "" || HexID("xyz", 16) != "" ||
		HexID("0af76519-16cd-43dd-8448-eb211c80319c", 32) == "" {
		t.Error("hex ids")
	}
	if levelName(slog.LevelError+4) != "fatal" || severity(slog.LevelDebug) != 5 {
		t.Error("levels")
	}
}
