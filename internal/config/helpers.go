// internal/config/helpers.go
package config

import (
	"fmt"
	"net"
	"strconv"
)

// ParseListenPort is an exported helper that parses a "host:port" string
// and returns the port as uint16.
func ParseListenPort(listen string) (uint16, error) {
	return parseListenPort(listen)
}

// parseListenPort parses "host:port" and returns the port as uint16.
func parseListenPort(listen string) (uint16, error) {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("invalid listen address (expected host:port): %w", err)
	}

	n, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range: %d", n)
	}

	return uint16(n), nil
}
