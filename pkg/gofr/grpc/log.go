package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Logger interface {
	Info(args ...interface{})
}

type RPCLog struct {
	ID           string `json:"id"`
	StartTime    string `json:"startTime"`
	ResponseTime int64  `json:"responseTime"`
	Method       string `json:"method"`
	StatusCode   int32  `json:"statusCode"`
}

func (l RPCLog) PrettyPrint(writer io.Writer) {
	// checking the length of status code to match the spacing that is being done in HTTP logs after status codes
	statusCodeLen := 9 - int(math.Log10(float64(l.StatusCode))) + 1

	fmt.Fprintf(writer, "\u001B[38;5;8m%s \u001B[38;5;%dm%-6d"+
		"\u001B[0m %*d\u001B[38;5;8mµs\u001B[0m %s \n",
		l.ID, colorForGRPCCode(l.StatusCode),
		l.StatusCode, statusCodeLen, l.ResponseTime, l.Method)
}

func colorForGRPCCode(s int32) int {
	const (
		blue = 34
		red  = 202
	)

	if s == 0 {
		return blue
	}

	return red
}

func (l RPCLog) String() string {
	line, _ := json.Marshal(l)
	return string(line)
}

func LoggingInterceptor(logger Logger) grpc.UnaryServerInterceptor {
	tracer := otel.GetTracerProvider().Tracer("gofr", trace.WithInstrumentationVersion("v0.1"))

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		ctx, _ = initializeSpanContext(ctx)
		ctx, span := tracer.Start(ctx, info.FullMethod)

		resp, err := handler(ctx, req)

		documentRPCLog(ctx, logger, info.FullMethod, start, err)

		span.End()

		return resp, err
	}
}

func initializeSpanContext(ctx context.Context) (context.Context, trace.SpanContext) {
	md, _ := metadata.FromIncomingContext(ctx)

	traceIDHex := getMetadataValue(md, "x-gofr-traceid")
	spanIDHex := getMetadataValue(md, "x-gofr-spanid")

	if traceIDHex == "" || spanIDHex == "" {
		return ctx, trace.SpanContext{}
	}

	traceID, _ := trace.TraceIDFromHex(traceIDHex)
	spanID, _ := trace.SpanIDFromHex(spanIDHex)

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})

	ctx = trace.ContextWithRemoteSpanContext(ctx, spanContext)

	return ctx, spanContext
}

func documentRPCLog(ctx context.Context, logger Logger, method string, start time.Time, err error) {
	logEntry := RPCLog{
		ID:           trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		StartTime:    start.Format("2006-01-02T15:04:05.999999999-07:00"),
		ResponseTime: time.Since(start).Microseconds(),
		Method:       method,
	}

	if err != nil {
		statusErr, _ := status.FromError(err)
		//nolint:gosec // gRPC codes are typically under the range.
		logEntry.StatusCode = int32(statusErr.Code())
	} else {
		logEntry.StatusCode = int32(codes.OK)
	}

	if logger != nil {
		logger.Info(logEntry)
	}
}

// Helper function to safely extract a value from metadata.
func getMetadataValue(md metadata.MD, key string) string {
	if values, ok := md[key]; ok && len(values) > 0 {
		return values[0]
	}

	return ""
}
