package httputil

import (
	"fmt"
	"net"
)

// MustParseCIDR parses a CIDR string and panics on failure.
// Intended for use in package-level variable initialization.
func MustParseCIDR(value string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(value)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", value, err))
	}
	return parsed
}
