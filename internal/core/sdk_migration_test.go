package core

// sdk_migration_test.go — guards for behaviour that had to be re-expressed when
// the microsandbox SDK changed shape. These assert msbd's OWN contract, which
// must survive an SDK bump even when the SDK's spelling of it does not.

import (
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// TestNetworkConfigPresets pins msbd's four public network-policy names.
//
// SDK 0.6.7 deleted the flat NetworkPolicyPreset enum these used to map onto
// 1:1 and replaced it with composable deny-by-default profiles. The preset
// names are part of msbd's REST contract (openapi.yaml + every generated
// client), so they must keep working AND keep meaning the same thing.
func TestNetworkConfigPresets(t *testing.T) {
	t.Run("none denies everything", func(t *testing.T) {
		c := networkConfig("none")
		if c == nil {
			t.Fatal("none returned nil")
		}
		if c.DefaultEgress != msb.PolicyActionDeny || c.DefaultIngress != msb.PolicyActionDeny {
			t.Errorf("none must deny both directions, got egress=%q ingress=%q",
				c.DefaultEgress, c.DefaultIngress)
		}
		if len(c.Rules) != 0 {
			t.Errorf("none must not punch any holes, got %d rules", len(c.Rules))
		}
	})

	t.Run("allow-all permits everything", func(t *testing.T) {
		c := networkConfig("allow-all")
		if c == nil {
			t.Fatal("allow-all returned nil")
		}
		if c.DefaultEgress != msb.PolicyActionAllow || c.DefaultIngress != msb.PolicyActionAllow {
			t.Errorf("allow-all must allow both directions, got egress=%q ingress=%q",
				c.DefaultEgress, c.DefaultIngress)
		}
	})

	t.Run("public-only allows public egress but not private", func(t *testing.T) {
		c := networkConfig("public-only")
		if c == nil {
			t.Fatal("public-only returned nil")
		}
		if c.DefaultEgress != msb.PolicyActionDeny {
			t.Errorf("public-only must stay deny-by-default, got %q", c.DefaultEgress)
		}
		if !hasEgressTo(c, string(msb.NetworkProfilePublic)) {
			t.Error("public-only must allow egress to the public profile")
		}
		if hasEgressTo(c, string(msb.NetworkProfilePrivate)) {
			t.Error("public-only must NOT allow egress to private/RFC-1918 ranges")
		}
		if hasEgressTo(c, string(msb.NetworkProfileHost)) {
			t.Error("public-only must NOT allow general egress to the host")
		}
		if !allowsGatewayDNS(c) {
			t.Error("public-only still needs the gateway-DNS carve-out to resolve names")
		}
	})

	t.Run("non-local allows public and private but never host", func(t *testing.T) {
		c := networkConfig("non-local")
		if c == nil {
			t.Fatal("non-local returned nil")
		}
		if !hasEgressTo(c, string(msb.NetworkProfilePublic)) {
			t.Error("non-local must allow public egress")
		}
		if !hasEgressTo(c, string(msb.NetworkProfilePrivate)) {
			t.Error("non-local must allow private/LAN egress")
		}
		// The old preset documented "blocks loopback, link-local, and metadata".
		if hasEgressTo(c, string(msb.NetworkProfileHost)) {
			t.Error("non-local must NOT reach the host generally (loopback/link-local/metadata)")
		}
		if !allowsGatewayDNS(c) {
			t.Error("non-local still needs the gateway-DNS carve-out to resolve names")
		}
	})

	t.Run("underscore spellings and casing are accepted", func(t *testing.T) {
		for _, name := range []string{"PUBLIC-ONLY", "public_only", " non_local ", "None"} {
			if networkConfig(name) == nil {
				t.Errorf("networkConfig(%q) = nil, want a config", name)
			}
		}
	})

	t.Run("unknown and empty fall through to the SDK default", func(t *testing.T) {
		for _, name := range []string{"", "   ", "bogus"} {
			if c := networkConfig(name); c != nil {
				t.Errorf("networkConfig(%q) = %+v, want nil (SDK default)", name, c)
			}
		}
	})
}

// hasEgressTo reports whether the policy allows UNRESTRICTED egress to a
// profile. FromProfiles always adds a gateway-DNS carve-out (udp/tcp port 53 to
// "host") so name resolution works; that is not general host access, so a
// port-scoped rule does not count.
func hasEgressTo(c *msb.NetworkConfig, destination string) bool {
	for _, r := range c.Rules {
		if r.Action == msb.PolicyActionAllow &&
			r.Direction == msb.PolicyDirectionEgress &&
			r.Destination == destination &&
			r.Port == "" && len(r.Ports) == 0 {
			return true
		}
	}
	return false
}

// allowsGatewayDNS reports whether the policy keeps the port-53 carve-out that
// makes DNS resolution work inside the guest.
func allowsGatewayDNS(c *msb.NetworkConfig) bool {
	for _, r := range c.Rules {
		if r.Action == msb.PolicyActionAllow && r.Port == "53" {
			return true
		}
	}
	return false
}

// TestNewSnapshotNameIsUniqueAndPrefixed covers the other 0.6.7 migration:
// Snapshot.Create now REQUIRES a name, but msbd's API has always allowed an
// unnamed snapshot, so core mints one.
func TestNewSnapshotNameIsUniqueAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		n := newSnapshotName()
		if !strings.HasPrefix(n, "snap_") {
			t.Fatalf("name %q lacks the snap_ prefix", n)
		}
		if len(n) != len("snap_")+16 {
			t.Fatalf("name %q has unexpected length %d", n, len(n))
		}
		if seen[n] {
			t.Fatalf("duplicate generated snapshot name %q", n)
		}
		seen[n] = true
	}
}
