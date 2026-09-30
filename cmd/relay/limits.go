package main

import (
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// limitFlags are the abuse-limit flags of Docs/protocol/relay-hosted.md §2
// (ticket 4.0b). Each default is the spec's; every value must be positive.
type limitFlags struct {
	upgradesPerMin, upgradeBurst        int
	connsPerPrefix, authFailures        int
	unauthConns, maxConns               int
	keysPerPrefix, prefixEnvsPerMin     int
	keyEnvsPerMin, keyEnvBurst          int
	controlPerMin, reconnectsPerMin     int
	pairEnvelopes, senderEnvelopes      int
	prefixBytesPerMin, keyBytesPerMin   byteSize
	connBuffer, maxInflight             byteSize
	maxInflightEphemeral                byteSize // 0: --max-inflight / 8, at least 1 MiB
	frameReadTimeout                    time.Duration
	pairBytes, senderBytes, queueTotal  byteSize
	minFreeDisk                         byteSize
	redeliverPerKey, redeliverPerPrefix byteSize // per hour (R55-F2)

	clientIPHeader string
	trustedProxies []netip.Prefix
}

func (l *limitFlags) register(fs *flag.FlagSet) {
	fs.IntVar(&l.upgradesPerMin, "max-upgrades-per-min", 30, "new WebSocket upgrades per client /24 (/48 IPv6) prefix per minute; past it HTTP 429")
	fs.IntVar(&l.upgradeBurst, "upgrade-burst", 60, "burst allowance for --max-upgrades-per-min")
	fs.IntVar(&l.connsPerPrefix, "max-conns-per-prefix", 64, "concurrent connections per client prefix; past it HTTP 429")
	fs.IntVar(&l.authFailures, "max-auth-failures", 10, "failed authentications per client prefix per 10 minutes; past it HTTP 429 for the rest of the window")
	fs.IntVar(&l.unauthConns, "max-unauth-conns", 256, "connections still authenticating, relay-wide; past it HTTP 503")
	fs.IntVar(&l.maxConns, "max-conns", 5000, "authenticated connections, relay-wide; past it error relay_full and close 1013")
	fs.IntVar(&l.keysPerPrefix, "max-keys-per-prefix", 64, "distinct authenticated keys connected from one client prefix; past it relay_full")
	fs.IntVar(&l.prefixEnvsPerMin, "prefix-envelopes-per-min", 600, "non-ephemeral envelopes per minute from all keys of a client prefix together; past it rate_limited")
	l.prefixBytesPerMin = 64 << 20
	fs.Var(&l.prefixBytesPerMin, "prefix-bytes-per-min", "bytes per minute sent by all keys of a client prefix together (e.g. 64MiB); past it rate_limited")
	fs.IntVar(&l.keyEnvsPerMin, "key-envelopes-per-min", 120, "non-ephemeral envelopes per minute from one key; past it rate_limited")
	fs.IntVar(&l.keyEnvBurst, "key-envelope-burst", 240, "burst allowance for --key-envelopes-per-min")
	l.keyBytesPerMin = 32 << 20
	fs.Var(&l.keyBytesPerMin, "key-bytes-per-min", "bytes per minute of non-ephemeral envelopes from one key (e.g. 32MiB); past it rate_limited")
	fs.IntVar(&l.controlPerMin, "control-per-min", 60, "control frames (ack excluded) per minute from one key; past it rate_limited, 3 minutes in a row close 1008")
	fs.IntVar(&l.reconnectsPerMin, "reconnects-per-min", 20, "authentications per minute of one key; past it rate_limited and close 1013")
	l.connBuffer = 4 << 20
	fs.Var(&l.connBuffer, "conn-buffer", "bytes waiting in one connection's outbound buffer (e.g. 4MiB); past it envelopes take the offline queue")
	l.maxInflight = 256 << 20
	fs.Var(&l.maxInflight, "max-inflight", "bytes waiting in all outbound buffers together (e.g. 256MiB); past it direct sends take the offline queue. Frames being read get a second budget of the same size")
	fs.Var(&l.maxInflightEphemeral, "max-inflight-ephemeral", "bytes of presence and control frames waiting in all outbound buffers together (e.g. 6MiB); past it they are dropped (default --max-inflight / 8, at least 1MiB)")
	fs.DurationVar(&l.frameReadTimeout, "frame-read-timeout", 30*time.Second, "time a peer has to send one whole frame from its first byte; past it the connection is closed 1013")
	fs.IntVar(&l.pairEnvelopes, "queue-pair-max-envelopes", 300, "envelopes one sender may have queued for one recipient; past it queue_full")
	l.pairBytes = 8 << 20
	fs.Var(&l.pairBytes, "queue-pair-max-bytes", "bytes one sender may have queued for one recipient (e.g. 8MiB); past it queue_full")
	fs.IntVar(&l.senderEnvelopes, "queue-sender-max-envelopes", 2000, "envelopes one sender may have queued for all recipients; past it queue_full")
	l.senderBytes = 64 << 20
	fs.Var(&l.senderBytes, "queue-sender-max-bytes", "bytes one sender may have queued for all recipients (e.g. 64MiB); past it queue_full")
	l.queueTotal = 4 << 30
	fs.Var(&l.queueTotal, "queue-max-total", "bytes of queued envelopes relay-wide (e.g. 4GiB); past it queue_full")
	l.minFreeDisk = 1 << 30
	fs.Var(&l.minFreeDisk, "queue-min-free-disk", "free disk under the queue file below which new envelopes get internal (\"relay storage low\"), e.g. 1GiB")
	l.redeliverPerKey = 32 << 20
	fs.Var(&l.redeliverPerKey, "queue-redeliver-per-key", "bytes of queued envelopes sent again to one recipient key that did not ack them, per hour (e.g. 32MiB, at least 1MiB: one frame); past it the redelivery waits for the budget to refill. First deliveries are not counted")
	l.redeliverPerPrefix = 128 << 20
	fs.Var(&l.redeliverPerPrefix, "queue-redeliver-per-prefix", "bytes of queued envelopes sent again to all recipients on one client prefix together, per hour (e.g. 128MiB, at least 1MiB: one frame); past it the redelivery waits its turn")
	fs.StringVar(&l.clientIPHeader, "client-ip-header", "", "with --behind-proxy (required there): the header carrying the client IP, e.g. Fly-Client-IP or X-Forwarded-For (its last entry); honoured only from a --trusted-proxy peer")
	fs.Func("trusted-proxy", "CIDR or IP of the proxy in front of this relay (repeatable; required with --client-ip-header); --client-ip-header from any other peer is ignored", func(v string) error {
		p, err := parseProxy(v)
		if err != nil {
			return err
		}
		l.trustedProxies = append(l.trustedProxies, p)
		return nil
	})
}

// parseProxy reads a CIDR, or a bare IP as a single-address prefix.
func parseProxy(v string) (netip.Prefix, error) {
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("not a CIDR: %w", err)
		}
		if p.Addr().Is4In6() {
			return netip.Prefix{}, errors.New("write an IPv4 CIDR in its IPv4 form")
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("not an IP or CIDR: %w", err)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// validate checks the limit and proxy flags against --behind-proxy.
func (l *limitFlags) validate(behindProxy bool) error {
	for _, f := range []struct {
		name string
		v    int64
	}{
		{"--max-upgrades-per-min", int64(l.upgradesPerMin)}, {"--upgrade-burst", int64(l.upgradeBurst)},
		{"--max-conns-per-prefix", int64(l.connsPerPrefix)}, {"--max-auth-failures", int64(l.authFailures)},
		{"--max-unauth-conns", int64(l.unauthConns)}, {"--max-conns", int64(l.maxConns)},
		{"--max-keys-per-prefix", int64(l.keysPerPrefix)}, {"--prefix-envelopes-per-min", int64(l.prefixEnvsPerMin)},
		{"--prefix-bytes-per-min", int64(l.prefixBytesPerMin)}, {"--key-envelopes-per-min", int64(l.keyEnvsPerMin)},
		{"--key-envelope-burst", int64(l.keyEnvBurst)}, {"--key-bytes-per-min", int64(l.keyBytesPerMin)},
		{"--control-per-min", int64(l.controlPerMin)}, {"--reconnects-per-min", int64(l.reconnectsPerMin)},
		{"--conn-buffer", int64(l.connBuffer)}, {"--max-inflight", int64(l.maxInflight)},
		{"--queue-pair-max-envelopes", int64(l.pairEnvelopes)}, {"--queue-pair-max-bytes", int64(l.pairBytes)},
		{"--queue-sender-max-envelopes", int64(l.senderEnvelopes)}, {"--queue-sender-max-bytes", int64(l.senderBytes)},
		{"--queue-max-total", int64(l.queueTotal)}, {"--queue-min-free-disk", int64(l.minFreeDisk)},
		{"--queue-redeliver-per-key", int64(l.redeliverPerKey)}, {"--queue-redeliver-per-prefix", int64(l.redeliverPerPrefix)},
	} {
		if f.v <= 0 {
			return fmt.Errorf("%s must be positive", f.name)
		}
	}
	// A bucket smaller than a frame could never pay for a large one, which
	// would then wait until it expires (review 74 L-1).
	for _, f := range []struct {
		name string
		v    byteSize
	}{{"--queue-redeliver-per-key", l.redeliverPerKey}, {"--queue-redeliver-per-prefix", l.redeliverPerPrefix}} {
		if f.v < envelope.MaxFrameBytes {
			return fmt.Errorf("%s must be at least 1MiB (one frame), got %s", f.name, f.v.String())
		}
	}
	if l.frameReadTimeout <= 0 {
		return errors.New("--frame-read-timeout must be positive")
	}
	switch {
	case behindProxy && l.clientIPHeader == "":
		return errors.New("--behind-proxy needs --client-ip-header (and --trusted-proxy): without the client IP every client shares the proxy's address and the per-prefix limits lock everyone out together")
	case !behindProxy && l.clientIPHeader != "":
		return errors.New("--client-ip-header needs --behind-proxy")
	case l.clientIPHeader != "" && len(l.trustedProxies) == 0:
		return errors.New("--client-ip-header needs at least one --trusted-proxy CIDR: the header is honoured only from those peers")
	case l.clientIPHeader == "" && len(l.trustedProxies) > 0:
		return errors.New("--trusted-proxy needs --client-ip-header")
	}
	return nil
}

// apply copies the flags into relay options.
func (l *limitFlags) apply(o *relay.Options) {
	o.UpgradesPerMinute, o.UpgradeBurst = l.upgradesPerMin, l.upgradeBurst
	o.MaxConnsPerPrefix, o.AuthFailuresPerPrefix = l.connsPerPrefix, l.authFailures
	o.MaxUnauthConns, o.MaxConns, o.MaxKeysPerPrefix = l.unauthConns, l.maxConns, l.keysPerPrefix
	o.PrefixEnvelopesPerMinute, o.PrefixBytesPerMinute = l.prefixEnvsPerMin, int64(l.prefixBytesPerMin)
	o.KeyEnvelopesPerMinute, o.KeyEnvelopeBurst, o.KeyBytesPerMinute = l.keyEnvsPerMin, l.keyEnvBurst, int64(l.keyBytesPerMin)
	o.ControlPerMinute, o.ReconnectsPerMinute = l.controlPerMin, l.reconnectsPerMin
	o.ConnBufferBytes, o.MaxInflight = int64(l.connBuffer), int64(l.maxInflight)
	o.MaxInflightEphemeral, o.FrameReadTimeout = int64(l.maxInflightEphemeral), l.frameReadTimeout
	o.QueuePairMaxEnvelopes, o.QueuePairMaxBytes = l.pairEnvelopes, int64(l.pairBytes)
	o.QueueSenderMaxEnvelopes, o.QueueSenderMaxBytes = l.senderEnvelopes, int64(l.senderBytes)
	o.QueueMaxTotal, o.QueueMinFreeDisk = int64(l.queueTotal), int64(l.minFreeDisk)
	o.QueueRedeliverPerKey, o.QueueRedeliverPerPrefix = int64(l.redeliverPerKey), int64(l.redeliverPerPrefix)
	o.ClientIPHeader, o.TrustedProxies = l.clientIPHeader, l.trustedProxies
}

// byteSize is a flag.Value of bytes: a plain integer or one with a KiB, MiB
// or GiB suffix (KB/MB/GB are read as the same binary units).
type byteSize int64

func (b *byteSize) String() string {
	v := int64(*b)
	for _, u := range []struct {
		name  string
		shift uint
	}{{"GiB", 30}, {"MiB", 20}, {"KiB", 10}} {
		if v != 0 && v%(1<<u.shift) == 0 {
			return strconv.FormatInt(v>>u.shift, 10) + u.name
		}
	}
	return strconv.FormatInt(v, 10)
}

func (b *byteSize) Set(s string) error {
	t := strings.TrimSpace(s)
	shift := uint(0)
	for _, u := range []struct {
		suffix string
		shift  uint
	}{{"GiB", 30}, {"GB", 30}, {"G", 30}, {"MiB", 20}, {"MB", 20}, {"M", 20}, {"KiB", 10}, {"KB", 10}, {"K", 10}, {"B", 0}} {
		if len(t) > len(u.suffix) && strings.EqualFold(t[len(t)-len(u.suffix):], u.suffix) {
			t, shift = strings.TrimSpace(t[:len(t)-len(u.suffix)]), u.shift
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 0 || n > (1<<62)>>shift {
		return fmt.Errorf("%q is not a byte size (e.g. 4MiB, 1GiB, 1048576)", s)
	}
	*b = byteSize(n << shift)
	return nil
}
