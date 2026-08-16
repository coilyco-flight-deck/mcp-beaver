package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type telemetryHarness struct {
	exporter *tracetest.InMemoryExporter
	reader   *sdkmetric.ManualReader
}

func useInMemoryTelemetry(t *testing.T) telemetryHarness {
	t.Helper()
	oldTracer := otel.GetTracerProvider()
	oldMeter := otel.GetMeterProvider()
	oldPropagator := otel.GetTextMapPropagator()
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(oldTracer)
		otel.SetMeterProvider(oldMeter)
		otel.SetTextMapPropagator(oldPropagator)
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return telemetryHarness{exporter: exporter, reader: reader}
}

func TestMCPServerTelemetrySuccessAndToolError(t *testing.T) {
	harness := useInMemoryTelemetry(t)
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "telemetry-token")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	sessionID := initializeTelemetrySession(t, ts)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-flight-deck","id":"42"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"outside-policy","id":"42"}}}`,
	} {
		resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, body)
		out := decodeRPCResponse(t, resp)
		if out.Error != nil {
			t.Fatalf("tools/call JSON-RPC error: %+v", out.Error)
		}
	}

	var calls []tracetest.SpanStub
	for _, span := range harness.exporter.GetSpans() {
		if span.Name == "tools/call get_thing" && span.InstrumentationScope.Name == instrumentationScope {
			calls = append(calls, span)
		}
		if span.Name == "execute_tool get_thing" {
			t.Fatal("MCP tools/call emitted a duplicate logical execute_tool span")
		}
	}
	if len(calls) != 2 {
		t.Fatalf("MCP call spans = %d, want 2", len(calls))
	}
	var successes, failures int
	for _, span := range calls {
		if span.SpanKind != trace.SpanKindServer {
			t.Errorf("span kind = %v, want SERVER", span.SpanKind)
		}
		switch span.Status.Code {
		case codes.Unset:
			successes++
		case codes.Error:
			failures++
			if got := spanAttribute(span, "error.type"); got != "tool_error" {
				t.Errorf("error.type = %q, want tool_error", got)
			}
			if !hasExceptionType(span, "tool_error") {
				t.Error("failed tool span omitted the closed-set exception classification")
			}
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("successful spans = %d, failed spans = %d", successes, failures)
	}

	histogram := collectHistogram(t, harness.reader, "mcp.server.operation.duration")
	var callCount uint64
	for _, point := range histogram.DataPoints {
		if setString(point.Attributes, "mcp.method.name") == "tools/call" {
			callCount += point.Count
			if !reflect.DeepEqual(point.Bounds, mcpDurationBuckets) {
				t.Errorf("duration bounds = %v, want %v", point.Bounds, mcpDurationBuckets)
			}
		}
	}
	if callCount != 2 {
		t.Fatalf("tools/call metric count = %d, want 2", callCount)
	}
}

func TestDirectHTTPToolTelemetryAndHealthExclusion(t *testing.T) {
	harness := useInMemoryTelemetry(t)
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "http-telemetry-token")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_thing", "application/json", `{"owner":"coilyco-flight-deck","id":"42"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	_ = decodeAPIResult(t, resp)
	spans := harness.exporter.GetSpans()
	execute := findSpan(t, spans, "execute_tool get_thing", trace.SpanKindInternal, instrumentationScope)
	if !execute.Parent.IsValid() {
		t.Fatal("execute_tool span has no HTTP server parent")
	}
	if !containsSpanContext(spans, execute.Parent, otelhttp.ScopeName) {
		t.Fatal("execute_tool parent is not the standard HTTP server span")
	}

	harness.exporter.Reset()
	health, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if got := len(harness.exporter.GetSpans()); got != 0 {
		t.Fatalf("health request emitted %d spans, want none", got)
	}
}

func TestRuntimeConstructorsShareToolInstrumentation(t *testing.T) {
	harness := useInMemoryTelemetry(t)
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "constructor-token")
	localUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer localUpstream.Close()
	proxyUpstream := newUpstreamServer(t, upstreamTool(t, "browse", "browse", `{"type":"object","properties":{}}`, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	}))
	defer proxyUpstream.Close()

	specServer, err := New("spec", "spec.mcp.kdl", []byte(roundTripSpec(localUpstream.URL)))
	if err != nil {
		t.Fatal(err)
	}
	ssmServer, err := newSSMServer("ssm", "ssm.mcp.kdl", ssmPolicy{Region: "us-east-1", Parameter: "/allowed"}, &fakeSSM{})
	if err != nil {
		t.Fatal(err)
	}
	proxyServer, err := NewProxy(context.Background(), "proxy", "", proxyUpstream.URL+"/mcp", []string{"browse"}, proxyUpstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer proxyServer.Close()

	tests := []struct {
		name   string
		server *Server
		path   string
		body   string
		mode   string
	}{
		{name: "spec", server: specServer, path: "/api/get_thing", body: `{"owner":"coilyco-flight-deck","id":"42"}`, mode: "spec"},
		{name: "ssm", server: ssmServer, path: "/api/get_parameter", body: `{"name":"/allowed"}`, mode: "ssm"},
		{name: "proxy", server: proxyServer, path: "/api/browse", body: `{}`, mode: "upstream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness.exporter.Reset()
			ts := httptest.NewServer(test.server.Handler())
			defer ts.Close()
			resp := postAPI(t, ts.Client(), ts.URL+test.path, "application/json", test.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, decodeAPIResult(t, resp))
			}
			_ = decodeAPIResult(t, resp)
			var logical *tracetest.SpanStub
			for _, span := range harness.exporter.GetSpans() {
				if strings.HasPrefix(span.Name, "execute_tool ") && span.InstrumentationScope.Name == instrumentationScope {
					copy := span
					logical = &copy
				}
			}
			if logical == nil {
				t.Fatal("constructor did not install shared logical tool instrumentation")
			}
			if got := spanAttribute(*logical, "mcp_beaver.mode"); got != test.mode {
				t.Fatalf("mcp_beaver.mode = %q, want %q", got, test.mode)
			}
		})
	}
}

func TestProxyTelemetryPropagationAndParentage(t *testing.T) {
	harness := useInMemoryTelemetry(t)
	var mu sync.Mutex
	captured := map[string][]map[string]any{}
	upstream := upstreamTool(t, "browse", "browse", `{"type":"object","properties":{"q":{"type":"string"}}}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args["q"].(string)}}}, nil, nil
	})
	upstream.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if params := requestParams(req, method, false); params != nil {
				meta := map[string]any{}
				for key, value := range params.GetMeta() {
					meta[key] = value
				}
				mu.Lock()
				captured[method] = append(captured[method], meta)
				mu.Unlock()
			}
			return next(ctx, method, req)
		}
	})
	upstreamTS := newUpstreamServer(t, upstream)
	defer upstreamTS.Close()
	proxy, err := NewProxy(context.Background(), "proxy", "", upstreamTS.URL+"/mcp", []string{"browse"}, upstreamTS.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxyTS := httptest.NewServer(proxy.Handler())
	defer proxyTS.Close()
	sessionID := initializeTelemetrySession(t, proxyTS)
	harness.exporter.Reset()
	mu.Lock()
	captured = map[string][]map[string]any{}
	mu.Unlock()

	const (
		mcpTraceID   = "11111111111111111111111111111111"
		mcpParentID  = "2222222222222222"
		httpTraceID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		httpParentID = "bbbbbbbbbbbbbbbb"
	)
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"browse","arguments":{"q":"ramen"},"_meta":{"traceparent":"00-` + mcpTraceID + `-` + mcpParentID + `-01","baggage":"tenant=test"}}}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, proxyTS.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-03-26")
	req.Header.Set("Mcp-Session-Id", sessionID)
	req.Header.Set("traceparent", "00-"+httpTraceID+"-"+httpParentID+"-01")
	resp, err := proxyTS.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := decodeRPCResponse(t, resp)
	if out.Error != nil {
		t.Fatalf("proxy call error: %+v", out.Error)
	}

	spans := harness.exporter.GetSpans()
	serverSpan := findSpan(t, spans, "tools/call browse", trace.SpanKindServer, instrumentationScope)
	if serverSpan.Parent.TraceID().String() != mcpTraceID || serverSpan.Parent.SpanID().String() != mcpParentID || !serverSpan.Parent.IsRemote() {
		t.Fatalf("server parent = %s/%s remote=%t", serverSpan.Parent.TraceID(), serverSpan.Parent.SpanID(), serverSpan.Parent.IsRemote())
	}
	var linkedHTTP bool
	for _, link := range serverSpan.Links {
		if containsSpanContext(spans, link.SpanContext, otelhttp.ScopeName) {
			linkedHTTP = true
		}
	}
	if !linkedHTTP {
		t.Fatalf("MCP server span did not link the ambient HTTP transport span: links=%v spans=%v", serverSpan.Links, spanNames(spans))
	}

	for _, name := range []string{"tools/list", "tools/call browse"} {
		clientSpan := findSpan(t, spans, name, trace.SpanKindClient, instrumentationScope)
		if clientSpan.Parent.SpanID() != serverSpan.SpanContext.SpanID() || clientSpan.SpanContext.TraceID() != serverSpan.SpanContext.TraceID() {
			t.Fatalf("%s client parent = %s/%s, want server span %s/%s", name, clientSpan.Parent.TraceID(), clientSpan.Parent.SpanID(), serverSpan.SpanContext.TraceID(), serverSpan.SpanContext.SpanID())
		}
		method := strings.TrimSuffix(name, " browse")
		mu.Lock()
		metas := append([]map[string]any(nil), captured[method]...)
		mu.Unlock()
		if len(metas) == 0 {
			t.Fatalf("upstream %s received no _meta", method)
		}
		meta := metas[len(metas)-1]
		traceparent, _ := meta["traceparent"].(string)
		if !strings.Contains(traceparent, clientSpan.SpanContext.SpanID().String()) {
			t.Fatalf("upstream %s traceparent = %q, want client span %s", method, traceparent, clientSpan.SpanContext.SpanID())
		}
		if baggage, _ := meta["baggage"].(string); baggage != "tenant=test" {
			t.Fatalf("upstream %s baggage = %q, want tenant=test", method, baggage)
		}
	}

	clientHistogram := collectHistogram(t, harness.reader, "mcp.client.operation.duration")
	var clientCalls uint64
	for _, point := range clientHistogram.DataPoints {
		if setString(point.Attributes, "mcp.method.name") == "tools/call" {
			clientCalls += point.Count
		}
	}
	if clientCalls != 1 {
		t.Fatalf("client tools/call metric count = %d, want 1", clientCalls)
	}
}

func TestTelemetryDoesNotCaptureSensitivePayloads(t *testing.T) {
	harness := useInMemoryTelemetry(t)
	const (
		tokenSentinel  = "secret-token-sentinel"
		argSentinel    = "secret-argument-sentinel"
		resultSentinel = "secret-result-sentinel"
		specSentinel   = "/private/spec/path-sentinel.mcp.kdl"
	)
	t.Setenv("MCP_BEAVER_TEST_TOKEN", tokenSentinel)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"` + resultSentinel + `"}`))
	}))
	defer upstream.Close()
	s, err := New("safe-server", specSentinel, []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := postAPI(t, ts.Client(), ts.URL+"/api/get_thing", "application/json", `{"owner":"coilyco-flight-deck","id":"42","search_query":"`+argSentinel+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, decodeAPIResult(t, resp))
	}
	_ = decodeAPIResult(t, resp)

	telemetryText := strings.Join(telemetryStrings(t, harness), "\n")
	for _, forbidden := range []string{tokenSentinel, argSentinel, resultSentinel, specSentinel, upstream.URL} {
		if strings.Contains(telemetryText, forbidden) {
			t.Fatalf("telemetry captured forbidden value %q:\n%s", forbidden, telemetryText)
		}
	}
}

func initializeTelemetrySession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"telemetry-test","version":"0.1.0"}}}`)
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if out := decodeRPCResponse(t, resp); out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}
	return sessionID
}

func findSpan(t *testing.T, spans tracetest.SpanStubs, name string, kind trace.SpanKind, scope string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name && span.SpanKind == kind && span.InstrumentationScope.Name == scope {
			return span
		}
	}
	t.Fatalf("no span name=%q kind=%v scope=%q in %v", name, kind, scope, spanNames(spans))
	return tracetest.SpanStub{}
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, fmt.Sprintf("%s/%s/%s/%s/%s", span.Name, span.SpanKind, span.InstrumentationScope.Name, span.SpanContext.TraceID(), span.SpanContext.SpanID()))
	}
	return names
}

func spanAttribute(span tracetest.SpanStub, key string) string {
	for _, attr := range span.Attributes {
		if string(attr.Key) == key {
			return fmt.Sprint(attr.Value.AsInterface())
		}
	}
	return ""
}

func hasExceptionType(span tracetest.SpanStub, want string) bool {
	for _, event := range span.Events {
		if event.Name != "exception" {
			continue
		}
		for _, attr := range event.Attributes {
			if attr.Key == attribute.Key("exception.type") && attr.Value.AsString() == want {
				return true
			}
		}
	}
	return false
}

func containsSpanContext(spans tracetest.SpanStubs, want trace.SpanContext, scope string) bool {
	for _, span := range spans {
		if span.SpanContext.TraceID() == want.TraceID() && span.SpanContext.SpanID() == want.SpanID() && span.InstrumentationScope.Name == scope {
			return true
		}
	}
	return false
}

func collectHistogram(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Histogram[float64] {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				histogram, ok := metric.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("metric %s data = %T, want float64 histogram", name, metric.Data)
				}
				return histogram
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.Histogram[float64]{}
}

func setString(set attribute.Set, key string) string {
	value, ok := set.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}

func telemetryStrings(t *testing.T, harness telemetryHarness) []string {
	t.Helper()
	var out []string
	for _, span := range harness.exporter.GetSpans() {
		out = append(out, span.Name)
		for _, attr := range span.Attributes {
			out = append(out, string(attr.Key), fmt.Sprint(attr.Value.AsInterface()))
		}
		for _, event := range span.Events {
			out = append(out, event.Name)
			for _, attr := range event.Attributes {
				out = append(out, string(attr.Key), fmt.Sprint(attr.Value.AsInterface()))
			}
		}
	}
	var metrics metricdata.ResourceMetrics
	if err := harness.reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			out = append(out, metric.Name, metric.Description)
			switch data := metric.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					for _, attr := range point.Attributes.ToSlice() {
						out = append(out, string(attr.Key), fmt.Sprint(attr.Value.AsInterface()))
					}
				}
			case metricdata.Histogram[int64]:
				for _, point := range data.DataPoints {
					for _, attr := range point.Attributes.ToSlice() {
						out = append(out, string(attr.Key), fmt.Sprint(attr.Value.AsInterface()))
					}
				}
			}
		}
	}
	return out
}
