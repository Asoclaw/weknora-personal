package utils

import (
	"context"
	"net"
	"testing"
)

func TestDNSRetryBoundedAndNXDomain(t *testing.T) {
	calls := 0
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		if calls < 3 {
			return nil, &net.DNSError{IsTemporary: true, Err: "temporary"}
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	ips, err := lookupIPAddrWithRetry(context.Background(), "example.com", lookup, 0)
	if err != nil || calls != 3 || len(ips) != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	_, err = lookupIPAddrWithRetry(context.Background(), "missing.invalid", func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		return nil, &net.DNSError{IsNotFound: true}
	}, 0)
	if err == nil || calls != 1 {
		t.Fatal("NXDOMAIN must not retry")
	}
	calls = 0
	_, err = lookupIPAddrWithRetry(context.Background(), "example.com", func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		return nil, &net.DNSError{IsTemporary: true}
	}, 0)
	if err == nil || calls != 3 {
		t.Fatal("retry budget exceeded")
	}
}

func TestSSRFCacheDoesNotKeepFailures(t *testing.T) {
	ResetSSRFWhitelistForTest()
	ResetSSRFOutboundValidationCacheForTest()
	for i := 0; i < 2; i++ {
		if validateURLForSSRFForOutbound("http://127.0.0.1/a") == nil {
			t.Fatal("restricted IP allowed")
		}
	}
	if SSRFOutboundValidationMissesForTest() != 2 {
		t.Fatal("failed validations must not cache")
	}
}
