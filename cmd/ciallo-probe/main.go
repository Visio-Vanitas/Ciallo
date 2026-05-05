package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"ciallo/internal/probe"
)

func main() {
	addr := flag.String("addr", "", "actual address to connect to, defaults to host:port")
	host := flag.String("host", "", "Minecraft handshake host name")
	port := flag.Uint("port", 25565, "Minecraft server port")
	protocol := flag.Int("protocol", int(probe.DefaultProtocolVersion), "Minecraft protocol version")
	timeout := flag.Duration("timeout", 5*time.Second, "probe timeout")
	jsonOutput := flag.Bool("json", false, "print JSON output")
	flag.Parse()

	result, err := probe.Run(context.Background(), probe.Options{
		Addr:            *addr,
		Host:            *host,
		Port:            uint16(*port),
		ProtocolVersion: int32(*protocol),
		Timeout:         *timeout,
	})
	if err != nil {
		if *jsonOutput {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"ok":    false,
				"host":  *host,
				"addr":  *addr,
				"error": err.Error(),
			})
		} else {
			fmt.Fprintf(os.Stderr, "probe failed: %v\n", err)
		}
		os.Exit(1)
	}

	if *jsonOutput {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":     true,
			"result": result,
		})
		return
	}
	fmt.Printf("host: %s\n", result.Host)
	fmt.Printf("addr: %s\n", result.Addr)
	fmt.Printf("protocol: %d\n", result.Protocol)
	fmt.Printf("duration_ms: %d\n", result.DurationMS)
	if len(result.Version) > 0 {
		fmt.Printf("version: %v\n", result.Version)
	}
	if len(result.Players) > 0 {
		fmt.Printf("players: %v\n", result.Players)
	}
	fmt.Printf("description: %s\n", result.Description)
}
