package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"ciallo/internal/mcproto"
)

const DefaultProtocolVersion int32 = 772

type Options struct {
	Addr            string
	Host            string
	Port            uint16
	ProtocolVersion int32
	Timeout         time.Duration
	MaxResponseSize int
}

type Result struct {
	Addr          string         `json:"addr"`
	Host          string         `json:"host"`
	Protocol      int32          `json:"protocol"`
	DurationMS    int64          `json:"duration_ms"`
	Version       map[string]any `json:"version,omitempty"`
	Players       map[string]any `json:"players,omitempty"`
	Description   string         `json:"description,omitempty"`
	RawJSONLength int            `json:"raw_json_length"`
}

func Run(ctx context.Context, options Options) (Result, error) {
	if strings.TrimSpace(options.Host) == "" {
		return Result{}, fmt.Errorf("host is required")
	}
	if options.Port == 0 {
		options.Port = 25565
	}
	if options.ProtocolVersion == 0 {
		options.ProtocolVersion = DefaultProtocolVersion
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.Addr == "" {
		options.Addr = net.JoinHostPort(options.Host, fmt.Sprintf("%d", options.Port))
	}
	if options.MaxResponseSize == 0 {
		options.MaxResponseSize = mcproto.MaxPacketLength
	}

	start := time.Now()
	var dialer net.Dialer
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", options.Addr)
	if err != nil {
		return Result{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(options.Timeout))

	handshake := mcproto.BuildHandshake(options.ProtocolVersion, options.Host, options.Port, mcproto.NextStateStatus)
	if err := mcproto.WritePacket(conn, handshake); err != nil {
		return Result{}, fmt.Errorf("write handshake: %w", err)
	}
	if err := mcproto.WritePacket(conn, mcproto.NewPacket(mcproto.StatusRequestPacketID, nil)); err != nil {
		return Result{}, fmt.Errorf("write status request: %w", err)
	}
	response, err := mcproto.ReadPacket(conn, options.MaxResponseSize)
	if err != nil {
		return Result{}, fmt.Errorf("read status response: %w", err)
	}
	statusJSON, err := mcproto.ParseStatusJSON(response)
	if err != nil {
		return Result{}, err
	}
	parsed, err := parseStatus(statusJSON)
	if err != nil {
		return Result{}, err
	}
	parsed.Addr = options.Addr
	parsed.Host = options.Host
	parsed.Protocol = options.ProtocolVersion
	parsed.DurationMS = time.Since(start).Milliseconds()
	parsed.RawJSONLength = len(statusJSON)
	return parsed, nil
}

func parseStatus(raw string) (Result, error) {
	var root struct {
		Version     map[string]any `json:"version"`
		Players     map[string]any `json:"players"`
		Description any            `json:"description"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return Result{}, fmt.Errorf("parse status json: %w", err)
	}
	return Result{
		Version:     root.Version,
		Players:     root.Players,
		Description: flattenDescription(root.Description),
	}, nil
}

func flattenDescription(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var out strings.Builder
		for _, item := range v {
			out.WriteString(flattenDescription(item))
		}
		return out.String()
	case map[string]any:
		var out strings.Builder
		if text, ok := v["text"].(string); ok {
			out.WriteString(text)
		}
		if extra, ok := v["extra"]; ok {
			out.WriteString(flattenDescription(extra))
		}
		if out.Len() > 0 {
			return out.String()
		}
		data, _ := json.Marshal(v)
		return string(data)
	default:
		return fmt.Sprint(v)
	}
}
