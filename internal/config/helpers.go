// internal/config/helpers.go
package config

// ParseListenPort is an exported helper that parses a "host:port" string
// and returns the port as uint16.
// Used by the engine builder to resolve listener ports from config.
func ParseListenPort(listen string) (uint16, error) {
	return parseListenPort(listen)
}
