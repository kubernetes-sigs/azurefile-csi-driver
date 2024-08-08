package azurefile

import "net"

// Resolver is an interface for resolving IP addresses.
type Resolver interface {
	ResolveIPAddr(network, address string) (*net.IPAddr, error)
}

// NetResolver is the real implementation of the Resolver interface.
type NetResolver struct{}

// ResolveIPAddr resolves the IP address using net.ResolveIPAddr.
func (r *NetResolver) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	return net.ResolveIPAddr(network, address)
}
