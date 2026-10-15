package utils

import (
	"context"
	"errors"
	"net"
	"time"
)

func lookupIPAddrWithRetry(ctx context.Context, host string, lookup func(context.Context, string) ([]net.IPAddr, error), delay time.Duration) ([]net.IPAddr, error) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var addresses []net.IPAddr
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		addresses, err = lookup(bounded, host)
		cancel()
		if err == nil {
			return addresses, nil
		}
		var dnsErr *net.DNSError
		if ctx.Err() != nil || !errors.As(err, &dnsErr) || dnsErr.IsNotFound {
			return nil, err
		}
		if attempt < 2 {
			timer := time.NewTimer(delay * time.Duration(attempt+1))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}
	}
	return nil, err
}

func lookupIPsForSecurity(host string) ([]net.IP, error) {
	addresses, err := lookupIPAddrWithRetry(context.Background(), host, net.DefaultResolver.LookupIPAddr, 250*time.Millisecond)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, address.IP)
	}
	return ips, nil
}
