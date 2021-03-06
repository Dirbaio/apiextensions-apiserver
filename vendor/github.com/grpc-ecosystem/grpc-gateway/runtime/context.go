package runtime

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/metadata"
)

// MetadataHeaderPrefix is prepended to HTTP headers in order to convert them to 
// gRPC metadata for incoming requests processed by grpc-gateway
const MetadataHeaderPrefix = "Grpc-Metadata-"
// MetadataTrailerPrefix is prepended to gRPC metadata as it is converted to
// HTTP headers in a response handled by grpc-gateway
const MetadataTrailerPrefix = "Grpc-Trailer-"
const metadataGrpcTimeout = "Grpc-Timeout"

const xForwardedFor = "X-Forwarded-For"
const xForwardedHost = "X-Forwarded-Host"

var (
	// DefaultContextTimeout is used for gRPC call context.WithTimeout whenever a Grpc-Timeout inbound
	// header isn't present. If the value is 0 the sent `context` will not have a timeout.
	DefaultContextTimeout = 0 * time.Second
)

/*
AnnotateContext adds context information such as metadata from the request.

At a minimum, the RemoteAddr is included in the fashion of "X-Forwarded-For",
except that the forwarded destination is not another HTTP service but rather
a gRPC service.
*/
func AnnotateContext(ctx context.Context, req *http.Request) (context.Context, error) {
	var pairs []string
	timeout := DefaultContextTimeout
	if tm := req.Header.Get(metadataGrpcTimeout); tm != "" {
		var err error
		timeout, err = timeoutDecode(tm)
		if err != nil {
			return nil, grpc.Errorf(codes.InvalidArgument, "invalid grpc-timeout: %s", tm)
		}
	}

	for key, vals := range req.Header {
		for _, val := range vals {
			if key == "Authorization" {
				pairs = append(pairs, "authorization", val)
				continue
			}
			if strings.HasPrefix(key, MetadataHeaderPrefix) {
				pairs = append(pairs, key[len(MetadataHeaderPrefix):], val)
			}
		}
	}
	if host := req.Header.Get(xForwardedHost); host != "" {
		pairs = append(pairs, strings.ToLower(xForwardedHost), host)
	} else if req.Host != "" {
		pairs = append(pairs, strings.ToLower(xForwardedHost), req.Host)
	}

	if addr := req.RemoteAddr; addr != "" {
		if remoteIP, _, err := net.SplitHostPort(addr); err == nil {
			if fwd := req.Header.Get(xForwardedFor); fwd == "" {
				pairs = append(pairs, strings.ToLower(xForwardedFor), remoteIP)
			} else {
				pairs = append(pairs, strings.ToLower(xForwardedFor), fmt.Sprintf("%s, %s", fwd, remoteIP))
			}
		} else {
			grpclog.Printf("invalid remote addr: %s", addr)
		}
	}

	if timeout != 0 {
		ctx, _ = context.WithTimeout(ctx, timeout)
	}
	if len(pairs) == 0 {
		return ctx, nil
	}
	return metadata.NewContext(ctx, metadata.Pairs(pairs...)), nil
}

// ServerMetadata consists of metadata sent from gRPC server.
type ServerMetadata struct {
	HeaderMD  metadata.MD
	TrailerMD metadata.MD
}

type serverMetadataKey struct{}

// NewServerMetadataContext creates a new context with ServerMetadata
func NewServerMetadataContext(ctx context.Context, md ServerMetadata) context.Context {
	return context.WithValue(ctx, serverMetadataKey{}, md)
}

// ServerMetadataFromContext returns the ServerMetadata in ctx
func ServerMetadataFromContext(ctx context.Context) (md ServerMetadata, ok bool) {
	md, ok = ctx.Value(serverMetadataKey{}).(ServerMetadata)
	return
}

func timeoutDecode(s string) (time.Duration, error) {
	size := len(s)
	if size < 2 {
		return 0, fmt.Errorf("timeout string is too short: %q", s)
	}
	d, ok := timeoutUnitToDuration(s[size-1])
	if !ok {
		return 0, fmt.Errorf("timeout unit is not recognized: %q", s)
	}
	t, err := strconv.ParseInt(s[:size-1], 10, 64)
	if err != nil {
		return 0, err
	}
	return d * time.Duration(t), nil
}

func timeoutUnitToDuration(u uint8) (d time.Duration, ok bool) {
	switch u {
	case 'H':
		return time.Hour, true
	case 'M':
		return time.Minute, true
	case 'S':
		return time.Second, true
	case 'm':
		return time.Millisecond, true
	case 'u':
		return time.Microsecond, true
	case 'n':
		return time.Nanosecond, true
	default:
	}
	return
}
